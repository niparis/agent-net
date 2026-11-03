package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// WorkerLog combines ended runs and current starts, with a hard owner-view limit.
func (s *Store) WorkerLog(limit int) ([]protocol.WorkerLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
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
	for _, run := range runs {
		r := run.entry
		if r.Caller, r.CallerHost, err = s.workerCaller(run.callerID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	agents, err := s.Agents(protocol.QueryAgents{IncludeOwners: true})
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
		caller, callerHost, err := s.workerCaller(w.Owner)
		if err != nil {
			return nil, err
		}
		out = append(out, protocol.WorkerLogEntry{SessionID: a.SessionID, NodeID: a.NodeID, Hostname: a.Hostname, Project: w.Project, Harness: a.Harness, Model: a.Model, Title: protocol.WorkerTitle(w.Title, w.Task, "-"), Difficulty: w.DifficultyUsed, Status: "running", StartedAt: w.StartedAt, RunSeconds: max(s.now().Unix()-w.StartedAt, 0), Assessment: w.Assessment, Caller: caller, CallerHost: callerHost})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		r := &out[i]
		// Match the start, not merely the session: continuations reuse a session.
		// worker_runs.started_at is Unix seconds, while a worker assessment's
		// started_at is Unix nanoseconds, taken when the judge was asked a few
		// seconds before the run. Take the latest assessment at or before the
		// run's start plus a small margin, never one from a later start.
		runStartNS := r.StartedAt * int64(time.Second)
		assessmentLimit := runStartNS + int64(5*time.Second)
		var raw string
		err = s.db.QueryRow(`SELECT json_object('run_id',run_id,'session_id',session_id,'started_at',started_at,'reasoning',reasoning,'scope',scope,'coupling',coupling,'uncertainty',uncertainty,'verification',verification,'risk',risk,'d',d,'label',label,'judge_harness',judge_harness,'judge_model',judge_model,'reasons',json(reasons),'picker_label',picker_label,'picker_note',picker_note,'error',error) FROM worker_assessments WHERE session_id=? AND started_at<=? ORDER BY started_at DESC, assessed_at DESC LIMIT 1`, r.SessionID, assessmentLimit).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			var a protocol.WorkerAssessment
			if err = json.Unmarshal([]byte(raw), &a); err != nil {
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
			var body string
			err = s.db.QueryRow(`SELECT body FROM messages WHERE sender=? AND created_at>=? AND created_at<=? AND body LIKE '[agent-net worker notice]%' AND body NOT LIKE '%status: difficulty%' AND body NOT LIKE '%status: started%' ORDER BY created_at LIMIT 1`, r.SessionID, r.StartedAt*1000000000, (r.EndedAt+60)*1000000000).Scan(&body)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
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
