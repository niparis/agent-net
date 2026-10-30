package gateway

import (
	"database/sql"
	"math"
	"sort"
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
	case protocol.OutcomeComplete, protocol.OutcomePartial, protocol.OutcomeFailed, protocol.OutcomeTimedOut:
	default:
		return reject("unknown worker outcome %q", r.Outcome)
	}
	if r.EndedAt == 0 {
		r.EndedAt = s.now().Unix()
	}
	if r.RunSeconds < 0 {
		r.RunSeconds = 0
	}
	_, err := s.db.Exec(`INSERT INTO worker_runs (node_id, session_id, harness, model, outcome, started_at, ended_at, run_seconds)
		VALUES (?,?,?,?,?,?,?,?)`, r.NodeID, r.SessionID, r.Harness, r.Model, r.Outcome, r.StartedAt, r.EndedAt, r.RunSeconds)
	return err
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
// model: the track record for that machine and the owner's playbook note.
// The track record is present even before the first run.
func (g *Gateway) choiceNotes(nodeID, harness, model string) (*protocol.TrackRecord, *protocol.PlaybookNote, error) {
	track, ok, err := g.store.TrackRecord(nodeID, harness, model, protocol.TrackRecordDays*24*time.Hour)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		track = &protocol.TrackRecord{Harness: harness, Model: model, Resumable: protocol.HarnessCanResume(harness)}
	}
	note, _, err := g.store.PlaybookFor(model, harness)
	if err != nil {
		return nil, nil, err
	}
	return track, note, nil
}

// SetPlaybook writes the owner's note about a model, or removes it with an
// empty note. An empty harness means the note applies to every harness.
func (s *Store) SetPlaybook(model, harness, note string) error {
	model, harness, note, err := protocol.CleanPlaybook(model, harness, note)
	if err != nil {
		return reject("%v", err)
	}
	if note == "" {
		_, err = s.db.Exec(`DELETE FROM playbook_notes WHERE model = ? AND harness = ?`, model, harness)
		return err
	}
	_, err = s.db.Exec(`INSERT INTO playbook_notes (model, harness, note, updated_at) VALUES (?,?,?,?)
		ON CONFLICT (model, harness) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at`,
		model, harness, note, s.now().Unix())
	return err
}

// PlaybookFor finds the note for a model and harness: the note written for
// that harness first, then the model-wide note (empty harness).
func (s *Store) PlaybookFor(model, harness string) (*protocol.PlaybookNote, bool, error) {
	model, harness, _, err := protocol.CleanPlaybook(model, harness, "")
	if err != nil {
		// An empty model has no note. Do not turn that into an error here: the
		// caller is choosing the harness default.
		if model == "" {
			return nil, false, nil
		}
		return nil, false, err
	}
	for _, h := range []string{harness, ""} {
		var n protocol.PlaybookNote
		var updated int64
		err := s.db.QueryRow(`SELECT model, harness, note, updated_at FROM playbook_notes WHERE model = ? AND harness = ?`,
			model, h).Scan(&n.Model, &n.Harness, &n.Note, &updated)
		if err == nil {
			n.UpdatedAt = updated
			return &n, true, nil
		}
		if err != sql.ErrNoRows {
			return nil, false, err
		}
		if h == "" {
			break
		}
	}
	return nil, false, nil
}
