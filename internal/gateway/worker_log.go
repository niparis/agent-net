package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerLogTables are the tables workerLogRead reads: the runs, the notices
// that a run's lookup finds (messages), and the agent rows of the workers
// (agentListTables, which covers the session, node and caller profile). Keep it
// equal to those queries (see readThrough).
var workerLogTables = tableUnion([]string{"worker_runs", "messages", "worker_brief_defects", "worker_usage", "model_prices"}, agentListTables)

// WorkerLog combines ended runs and current starts, with a hard owner-view limit.
//
// The read is kept until one of workerLogTables is written, but never past the
// second it was read in: a running worker's time and the windows of the agent
// rows depend on the clock. Each caller gets a copy.
func (s *Store) WorkerLog(limit int) ([]protocol.WorkerLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	v, err := s.readThrough("worker_log|"+strconv.Itoa(limit), workerLogTables, true, func() (any, error) { return s.workerLogRead(limit) })
	if err != nil {
		return nil, err
	}
	return copyOf(v.([]protocol.WorkerLogEntry)), nil
}

func (s *Store) workerLogRead(limit int) ([]protocol.WorkerLogEntry, error) {
	// worker_runs.started_at is Unix seconds. An assessment's started_at is
	// Unix nanoseconds; the join below converts explicitly.
	rows, err := s.db.Query(`SELECT r.session_id,r.node_id,COALESCE(n.hostname,''),COALESCE(NULLIF(s.repo_project,''),json_extract(CASE WHEN json_valid(s.worker) THEN s.worker ELSE '{}' END,'$.project'),''),r.harness,r.model,COALESCE(NULLIF(s.worker_title,''),s.title,''),COALESCE(NULLIF(r.difficulty_used,''),r.difficulty),r.outcome,r.started_at,r.run_seconds,r.ended_at,COALESCE(NULLIF(r.caller_session_id,''),json_extract(CASE WHEN json_valid(s.worker) THEN s.worker ELSE '{}' END,'$.owner'),'') FROM worker_runs r LEFT JOIN sessions s ON s.id=r.session_id LEFT JOIN nodes n ON n.id=r.node_id ORDER BY r.started_at DESC,r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := []protocol.WorkerLogEntry{}
	type runRow struct {
		entry    protocol.WorkerLogEntry
		callerID string
	}
	var runs []runRow
	for rows.Next() {
		var r protocol.WorkerLogEntry
		var callerID string
		if err = rows.Scan(&r.SessionID, &r.NodeID, &r.Hostname, &r.Project, &r.Harness, &r.Model, &r.Title, &r.Difficulty, &r.Status, &r.StartedAt, &r.RunSeconds, &r.EndedAt, &callerID); err != nil {
			rows.Close()
			return nil, err
		}
		switch r.Status {
		case protocol.OutcomeComplete:
			r.Status = "done"
		case protocol.OutcomePartial:
			r.Status = "blocked"
		case protocol.OutcomeTimedOut:
			r.Status = "timed out"
		}
		runs = append(runs, runRow{entry: r, callerID: callerID})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	seenCallers := map[string]bool{}
	var callerIDs []string
	for _, run := range runs {
		if run.callerID != "" && !seenCallers[run.callerID] {
			seenCallers[run.callerID] = true
			callerIDs = append(callerIDs, run.callerID)
		}
	}
	briefDefects, err := s.briefDefectsByCaller(callerIDs)
	if err != nil {
		return nil, err
	}
	// Many runs share a caller: look each one up once.
	type callerName struct{ name, host string }
	callers := map[string]callerName{}
	workerCaller := func(id string) (string, string, error) {
		if c, ok := callers[id]; ok {
			return c.name, c.host, nil
		}
		name, host, err := s.workerCaller(id)
		if err == nil {
			callers[id] = callerName{name, host}
		}
		return name, host, err
	}
	var ids []string
	for _, run := range runs {
		ids = append(ids, run.entry.SessionID)
	}
	usage, err := s.workerUsage(ids...)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		r := run.entry
		if t, ok := usage[r.SessionID]; ok {
			r.Tokens = &t
			cost, listed, ok := s.RunCost(protocol.PickerModelKey(r.Harness, r.Model), t)
			r.Cost, r.PriceUnknown = cost, ok && !listed
		}
		r.BriefDefects = briefDefects[run.callerID]
		if r.Caller, r.CallerHost, err = workerCaller(run.callerID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// Only the stored fields of online workers are used below.
	agents, err := s.agentsBase(protocol.QueryAgents{IncludeOwners: true, OnlineOnly: true})
	if err != nil {
		return nil, err
	}
	for _, a := range agents {
		if a.Worker == nil || !a.Online {
			continue
		}
		w := a.Worker
		// A stopped report can arrive before the session is marked offline.
		ended := false
		for _, r := range out {
			if r.SessionID == a.SessionID && r.StartedAt == w.StartedAt {
				ended = true
				break
			}
		}
		if ended {
			continue
		}
		caller, callerHost, err := workerCaller(w.Owner)
		if err != nil {
			return nil, err
		}
		out = append(out, protocol.WorkerLogEntry{SessionID: a.SessionID, NodeID: a.NodeID, Hostname: a.Hostname, Project: w.Project, Harness: a.Harness, Model: a.Model, Title: protocol.WorkerTitle(w.Title, w.Task, "-"), Difficulty: w.DifficultyUsed, Status: "running", StartedAt: w.StartedAt, RunSeconds: max(s.now().Unix()-w.StartedAt, 0), Assessment: w.Assessment, Caller: caller, CallerHost: callerHost, BriefDefects: briefDefects[w.Owner]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	lookups, err := s.workerRunLookups(out)
	if err != nil {
		return nil, err
	}
	for i := range out {
		r := &out[i]
		lookup := lookups[i]
		if lookup.assessment.Valid {
			var a protocol.WorkerAssessment
			if err = json.Unmarshal([]byte(lookup.assessment.String), &a); err != nil {
				return nil, err
			}
			r.Assessment = &a
			r.ModelNote = a.PickerNote
			if r.Difficulty == "" {
				r.Difficulty = a.PickerLabel
				if r.Difficulty == "" {
					r.Difficulty = a.Label
				}
			}
		}

		if r.Status != "running" {
			body := lookup.notice.String
			for _, line := range strings.Split(strings.Split(body, protocol.ReportMarker)[0], "\n") {
				if strings.HasPrefix(line, "worker: ") {
					if _, title, ok := strings.Cut(line, " · "); ok {
						r.Title = title
					}
				}
				if strings.HasPrefix(line, "note: ") {
					r.Failure = strings.TrimPrefix(line, "note: ")
				}
			}
		}
	}
	return out, nil
}

// briefDefectsByCaller counts the brief defects charged to each of the given
// briefing sessions. One statement for the whole log.
func (s *Store) briefDefectsByCaller(callers []string) (map[string]int, error) {
	out := map[string]int{}
	if len(callers) == 0 {
		return out, nil
	}
	args := make([]any, len(callers))
	for i, c := range callers {
		args[i] = c
	}
	rows, err := s.db.Query(`SELECT caller, COUNT(*) FROM worker_brief_defects WHERE caller IN (?`+strings.Repeat(",?", len(callers)-1)+`) GROUP BY caller`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var caller string
		var n int
		if err := rows.Scan(&caller, &n); err != nil {
			return nil, err
		}
		out[caller] = n
	}
	return out, rows.Err()
}

// workerCaller resolves the caller session of a run to the name and machine
// the owner sees. The owner's pseudo-session and the CTO keep their short
// names; a missing or deleted session reads empty.
func (s *Store) workerCaller(id string) (name, host string, err error) {
	if id == "" {
		return "", "", nil
	}
	var harness, sessionName, hostname string
	err = s.db.QueryRow(`SELECT s.harness, COALESCE(NULLIF(p.name,''), NULLIF(s.name,''), NULLIF(s.title,''), s.id), COALESCE(n.hostname,'')
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id LEFT JOIN nodes n ON n.id=s.node_id WHERE s.id=?`, id).Scan(&harness, &sessionName, &hostname)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	switch {
	case protocol.IsOwner(harness):
		sessionName = "owner"
	case harness == "cto":
		sessionName = "CTO"
	}
	return sessionName, hostname, nil
}

// workerRunLookup is what one run of the log needs besides its own row.
type workerRunLookup struct {
	assessment sql.NullString // the assessment, as JSON
	notice     sql.NullString // the body of the worker's final notice
}

// workerRunLookups finds, for every entry, its assessment and its notice in one
// statement. The log has up to 200 entries, and a statement per lookup costs
// more than the lookup.
//
// An assessment's started_at is Unix nanoseconds, taken when the judge was
// asked a few seconds before the run, while worker_runs.started_at is Unix
// seconds. The latest assessment at or before the run's start plus a small
// margin matches the run, never one from a later start. Continuations reuse a
// session, so the start matters, not only the session. A running entry has no
// notice yet.
func (s *Store) workerRunLookups(entries []protocol.WorkerLogEntry) ([]workerRunLookup, error) {
	out := make([]workerRunLookup, len(entries))
	if len(entries) == 0 {
		return out, nil
	}
	var q strings.Builder
	args := make([]any, 0, 5*len(entries))
	q.WriteString(`WITH q(i,sid,assessed_before,notice_from,notice_to) AS (VALUES `)
	for i, r := range entries {
		if i > 0 {
			q.WriteByte(',')
		}
		q.WriteString(`(?,?,?,?,?)`)
		noticeFrom, noticeTo := r.StartedAt*int64(time.Second), (r.EndedAt+60)*int64(time.Second)
		if r.Status == "running" {
			noticeFrom, noticeTo = 1, 0 // nothing is in this range
		}
		args = append(args, i, r.SessionID, r.StartedAt*int64(time.Second)+int64(5*time.Second), noticeFrom, noticeTo)
	}
	q.WriteString(`) SELECT q.i,
 (SELECT json_object('run_id',run_id,'session_id',session_id,'started_at',started_at,'reasoning',reasoning,'scope',scope,'coupling',coupling,'uncertainty',uncertainty,'verification',verification,'risk',risk,'d',d,'label',label,'judge_harness',judge_harness,'judge_model',judge_model,'reasons',json(reasons),'picker_label',picker_label,'picker_note',picker_note,'error',error)
  FROM worker_assessments WHERE session_id=q.sid AND started_at<=q.assessed_before ORDER BY started_at DESC, assessed_at DESC LIMIT 1),
 (SELECT body FROM messages WHERE sender=q.sid AND created_at>=q.notice_from AND created_at<=q.notice_to AND body LIKE '[agent-net worker notice]%' AND body NOT LIKE '%status: difficulty%' AND body NOT LIKE '%status: started%' ORDER BY created_at LIMIT 1)
 FROM q`)
	rows, err := s.db.Query(q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		var l workerRunLookup
		if err = rows.Scan(&i, &l.assessment, &l.notice); err != nil {
			return nil, err
		}
		out[i] = l
	}
	return out, rows.Err()
}
