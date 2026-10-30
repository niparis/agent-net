package gateway

import (
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The model track record and the owner's playbook notes (decision 0022).

// RecordWorkerRun adds one ended worker run. The gateway calls it when a
// machine reports "worker.stopped".
func (s *Store) RecordWorkerRun(r protocol.WorkerRun) error {
	if r.NodeID == "" || r.Harness == "" || r.Outcome == "" {
		return reject("a worker run needs a node, a harness and an outcome")
	}
	switch r.Outcome {
	case protocol.OutcomeComplete, protocol.OutcomePartial, protocol.OutcomeFailed, protocol.OutcomeTimedOut, protocol.OutcomeStopped:
	default:
		return reject("unknown worker outcome %q", r.Outcome)
	}
	if r.EndedAt == 0 {
		r.EndedAt = s.now().Unix()
	}
	if r.RunSeconds < 0 {
		r.RunSeconds = 0
	}
	if _, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, started_at, ended_at, run_seconds)
		VALUES (?,?,?,?,?,?,?,?)`, r.NodeID, r.SessionID, r.Harness, r.Model, r.Outcome, r.StartedAt, r.EndedAt, r.RunSeconds); err != nil {
		return err
	}
	// A clearly bad track record promotes a note on its own (decision 0022).
	if err := s.evaluatePlaybook(protocol.SubjectModel, r.Model, ""); err != nil {
		return err
	}
	if err := s.evaluatePlaybook(protocol.SubjectModelHarness, r.Model+"@"+r.Harness, ""); err != nil {
		return err
	}
	return s.evaluatePlaybook(protocol.SubjectHarness, r.Harness, "")
}

// TrackRecords counts the worker runs of the last window, per harness and
// model. A nodeID keeps one machine's runs; empty counts every machine.
func (s *Store) TrackRecords(nodeID string, window time.Duration) ([]protocol.TrackRecord, error) {
	if window <= 0 {
		window = protocol.TrackRecordDays * 24 * time.Hour
	}
	cut := s.now().Add(-window).Unix()
	query := `SELECT harness, model, outcome, run_seconds FROM worker_runs WHERE ended_at >= ?`
	args := []any{cut}
	if nodeID != "" {
		query += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	query += ` ORDER BY harness, model, ended_at`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ harness, model string }
	order := []key{}
	byKey := map[key]*protocol.TrackRecord{}
	durations := map[key][]int64{}
	for rows.Next() {
		var harness, model, outcome string
		var run int64
		if err := rows.Scan(&harness, &model, &outcome, &run); err != nil {
			return nil, err
		}
		k := key{harness, model}
		tr := byKey[k]
		if tr == nil {
			tr = &protocol.TrackRecord{Harness: harness, Model: model, Resumable: protocol.HarnessCanResume(harness)}
			byKey[k] = tr
			order = append(order, k)
		}
		tr.Runs++
		switch outcome {
		case protocol.OutcomeComplete:
			tr.Complete++
		case protocol.OutcomePartial:
			tr.Partial++
		case protocol.OutcomeFailed:
			tr.Failed++
		case protocol.OutcomeTimedOut:
			tr.TimedOut++
		case protocol.OutcomeStopped:
			tr.Stopped++
		}
		if run > 0 {
			durations[k] = append(durations[k], run)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]protocol.TrackRecord, 0, len(order))
	for _, k := range order {
		tr := byKey[k]
		tr.MedianRunSeconds = median(durations[k])
		out = append(out, *tr)
	}
	return out, nil
}

// TrackRecord returns the record of one harness and model, or ok false when
// it has no runs in the window.
func (s *Store) TrackRecord(nodeID, harness, model string, window time.Duration) (*protocol.TrackRecord, bool, error) {
	all, err := s.TrackRecords(nodeID, window)
	if err != nil {
		return nil, false, err
	}
	for i := range all {
		if all[i].Harness == harness && all[i].Model == model {
			return &all[i], true, nil
		}
	}
	return nil, false, nil
}

// TrackSubject counts the runs behind a playbook subject. Model counts every
// harness of that model; harness counts every model of that harness;
// model_harness is one pair. The record is network-wide.
func (s *Store) TrackSubject(kind, subject string, window time.Duration) (*protocol.TrackRecord, bool, error) {
	if window <= 0 {
		window = protocol.TrackRecordDays * 24 * time.Hour
	}
	cut := s.now().Add(-window).Unix()
	var query string
	var args []any
	tr := &protocol.TrackRecord{Harness: "all", Model: subject, Resumable: true}
	switch kind {
	case protocol.SubjectModel:
		query = `SELECT harness, outcome, run_seconds FROM worker_runs WHERE model = ? AND ended_at >= ?`
		args = []any{subject, cut}
	case protocol.SubjectHarness:
		query = `SELECT model, outcome, run_seconds FROM worker_runs WHERE harness = ? AND ended_at >= ?`
		args = []any{subject, cut}
		tr = &protocol.TrackRecord{Harness: subject, Model: "all", Resumable: protocol.HarnessCanResume(subject)}
	case protocol.SubjectModelHarness:
		model, harness, ok := protocol.ParseModelHarness(subject)
		if !ok {
			return nil, false, nil
		}
		query = `SELECT '', outcome, run_seconds FROM worker_runs WHERE model = ? AND harness = ? AND ended_at >= ?`
		args = []any{model, harness, cut}
		tr = &protocol.TrackRecord{Harness: harness, Model: model, Resumable: protocol.HarnessCanResume(harness)}
	default:
		return nil, false, nil
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var durations []int64
	for rows.Next() {
		var other, outcome string
		var run int64
		if err := rows.Scan(&other, &outcome, &run); err != nil {
			return nil, false, err
		}
		tr.Runs++
		switch outcome {
		case protocol.OutcomeComplete:
			tr.Complete++
		case protocol.OutcomePartial:
			tr.Partial++
		case protocol.OutcomeFailed:
			tr.Failed++
		case protocol.OutcomeTimedOut:
			tr.TimedOut++
		case protocol.OutcomeStopped:
			tr.Stopped++
		}
		if run > 0 {
			durations = append(durations, run)
		}
		if kind == protocol.SubjectModel && !protocol.HarnessCanResume(other) {
			tr.Resumable = false
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if tr.Runs == 0 {
		return nil, false, nil
	}
	tr.MedianRunSeconds = median(durations)
	return tr, true, nil
}

// median returns the middle run time, or 0 when there is none.
func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]int64(nil), values...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	m := len(v) / 2
	if len(v)%2 == 1 {
		return v[m]
	}
	return int64(math.Round(float64(v[m-1]+v[m]) / 2))
}

// WorkerSessionFacts reads the harness, model and start the gateway stored for
// a worker session. An older machine may send a worker.stopped without them.
func (s *Store) WorkerSessionFacts(sessionID string) (startedAt int64, harness, model string, ok bool, err error) {
	var ns int64
	err = s.db.QueryRow(`SELECT worker_started, harness, model FROM sessions WHERE id = ?`, sessionID).
		Scan(&ns, &harness, &model)
	if err == sql.ErrNoRows {
		return 0, "", "", false, nil
	}
	if err != nil {
		return 0, "", "", false, err
	}
	return ns / int64(time.Second), harness, model, true, nil
}

// choiceNotes is what the gateway tells a caller when it picks a harness and
// model: the track record for that machine and the fresh playbook notes for
// the model, harness, project and machine. The track record is present even
// before the first run.
func (g *Gateway) choiceNotes(nodeID, hostname, project, harness, model string) (*protocol.TrackRecord, []protocol.PlaybookNote, error) {
	track, ok, err := g.store.TrackRecord(nodeID, harness, model, protocol.TrackRecordDays*24*time.Hour)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		track = &protocol.TrackRecord{Harness: harness, Model: model, Resumable: protocol.HarnessCanResume(harness)}
	}
	notes, err := g.store.PlaybookForChoice(model, harness, project, hostname)
	if err != nil {
		return nil, nil, err
	}
	return track, notes, nil
}

// SetPlaybook writes the owner's note about a model, or removes it with an
// empty note. An empty harness means the note applies to every harness.
func (s *Store) SetPlaybook(model, harness, note string) error {
	return s.SetPlaybookPin(model, harness, note, nil)
}

// SetPlaybookPin is SetPlaybook with an optional pin value.
func (s *Store) SetPlaybookPin(model, harness, note string, pin *bool) error {
	model, harness, note, err := protocol.CleanPlaybook(model, harness, note)
	if err != nil {
		return reject("%v", err)
	}
	kind := protocol.SubjectModel
	if harness != "" {
		kind = protocol.SubjectModelHarness
	}
	if note == "" {
		_, err = s.db.Exec(`DELETE FROM playbook WHERE subject_kind = ? AND subject = ? AND harness = ?`, kind, model, harness)
		return err
	}
	now := s.now().Unix()
	pinned := 0
	if pin != nil && *pin {
		pinned = 1
	}
	_, err = s.db.Exec(`INSERT INTO playbook (subject_kind, subject, harness, note, source, support, track_line, pinned, stale, created_at, updated_at)
		VALUES (?,?,?,?,?,0,'',?,0,?,?)
		ON CONFLICT (subject_kind, subject, harness) DO UPDATE SET note=excluded.note, source='owner',
		support=0, track_line='', pinned=excluded.pinned, stale=0, updated_at=excluded.updated_at`,
		kind, model, harness, note, protocol.PlaybookSourceOwner, pinned, now, now)
	return err
}

// PlaybookNotes returns the fresh notes, newest first. An automatic note whose
// support is all older than PlaybookStaleDays goes stale and is left out. Owner
// notes and pinned notes never go stale.
func (s *Store) PlaybookNotes() ([]protocol.PlaybookNote, error) {
	now := s.now()
	cut := now.Add(-protocol.PlaybookStaleDays * 24 * time.Hour).Unix()
	if _, err := s.db.Exec(`UPDATE playbook SET stale=1 WHERE stale=0 AND source=? AND pinned=0 AND updated_at < ?`,
		protocol.PlaybookSourceAuto, cut); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT subject_kind, subject, harness, note, source, support, track_line, pinned, created_at, updated_at
		FROM playbook WHERE stale=0 ORDER BY updated_at DESC, subject_kind, subject, harness`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.PlaybookNote{}
	for rows.Next() {
		var n protocol.PlaybookNote
		var pinned int
		if err := rows.Scan(&n.SubjectKind, &n.Subject, &n.Harness, &n.Note, &n.Source, &n.Support,
			&n.TrackLine, &pinned, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.Pinned = pinned == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

// PlaybookForChoice returns the fresh notes that apply when a worker starts
// with this model and harness in this project on this machine.
func (s *Store) PlaybookForChoice(model, harness, project, machine string) ([]protocol.PlaybookNote, error) {
	notes, err := s.PlaybookNotes()
	if err != nil {
		return nil, err
	}
	out := []protocol.PlaybookNote{}
	for _, n := range notes {
		switch n.SubjectKind {
		case protocol.SubjectModelHarness:
			if n.Subject == model && strings.EqualFold(n.Harness, harness) {
				out = append(out, n)
			}
		case protocol.SubjectModel:
			if n.Subject == model && n.Harness == "" {
				out = append(out, n)
			}
		case protocol.SubjectHarness:
			if strings.EqualFold(n.Subject, harness) {
				out = append(out, n)
			}
		case protocol.SubjectProject:
			if project != "" && strings.EqualFold(n.Subject, project) {
				out = append(out, n)
			}
		case protocol.SubjectMachine:
			if machine != "" && strings.EqualFold(n.Subject, machine) {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// PlaybookFor finds the owner's note for a model and harness: the note for
// that harness first, then the model-wide note (empty harness).
func (s *Store) PlaybookFor(model, harness string) (*protocol.PlaybookNote, bool, error) {
	model, harness, _, err := protocol.CleanPlaybook(model, harness, "")
	if err != nil {
		if model == "" {
			return nil, false, nil
		}
		return nil, false, err
	}
	notes, err := s.PlaybookNotes()
	if err != nil {
		return nil, false, err
	}
	for _, n := range notes {
		if n.SubjectKind == protocol.SubjectModelHarness && n.Subject == model && strings.EqualFold(n.Harness, harness) {
			return &n, true, nil
		}
	}
	for _, n := range notes {
		if n.SubjectKind == protocol.SubjectModel && n.Subject == model && n.Harness == "" {
			return &n, true, nil
		}
	}
	return nil, false, nil
}

// RecentWorkerFailures returns models with a real failed run in the failure window.
// A stopped run is neutral and does not count as a model failure.
func (s *Store) RecentWorkerFailures(nodeID, harness string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT model FROM worker_runs WHERE node_id = ? AND harness = ? AND outcome = ? AND ended_at >= ?`, nodeID, harness, protocol.OutcomeFailed, s.now().Add(-protocol.ModelFailureHours*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		out[model] = true
	}
	return out, rows.Err()
}
