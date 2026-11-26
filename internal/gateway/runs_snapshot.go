package gateway

import "sort"

// runRow is one worker_runs row, with the columns the reads of the whole table
// use (track records and the picker).
type runRow struct {
	nodeID, session, task, model, harness, outcome string
	ended, runSeconds                              int64
	cont, fix, auto                                int
	early                                          bool
}

// runsSnapshot is a parsed copy of worker_runs: the rows in (ended_at, id)
// order. Nothing in it depends on the clock, so a window cut is applied by
// the reader. It is valid for one version of the table. The rows are shared
// between callers and must be treated as read only.
type runsSnapshot struct {
	version int64
	rows    []runRow
}

// runsSnapshot returns the copy of worker_runs for its current version. It
// reads one number per call and reloads the table only when SQLite bumped the
// version (the gateway_state key worker_runs_version), which a trigger does on
// every insert, update and delete.
func (s *Store) runsSnapshot() (*runsSnapshot, error) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	// The version is read before the rows. A write between the two reads leaves
	// a copy that is newer than its version, so the next call reloads it.
	var version int64
	if err := s.db.QueryRow(`SELECT COALESCE((SELECT CAST(value AS INTEGER) FROM gateway_state WHERE key='worker_runs_version'), 0)`).Scan(&version); err != nil {
		return nil, err
	}
	if s.runs != nil && s.runs.version == version {
		return s.runs, nil
	}
	snap, err := s.loadRunsSnapshot(version)
	if err != nil {
		return nil, err
	}
	s.runs = snap
	return snap, nil
}

func (s *Store) loadRunsSnapshot(version int64) (*runsSnapshot, error) {
	rows, err := s.db.Query(`SELECT node_id,session_id,task_id,model,harness,outcome,ended_at,run_seconds,continue_count,fix_count,auto_continue_count,early_quit FROM worker_runs ORDER BY ended_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snap := &runsSnapshot{version: version}
	for rows.Next() {
		var r runRow
		if err := rows.Scan(&r.nodeID, &r.session, &r.task, &r.model, &r.harness, &r.outcome, &r.ended, &r.runSeconds, &r.cont, &r.fix, &r.auto, &r.early); err != nil {
			return nil, err
		}
		snap.rows = append(snap.rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snap, nil
}

// since returns the rows that ended at or after cut. The rows are in end time
// order, so they are a tail of the slice.
func (snap *runsSnapshot) since(cut int64) []runRow {
	i := sort.Search(len(snap.rows), func(i int) bool { return snap.rows[i].ended >= cut })
	return snap.rows[i:]
}
