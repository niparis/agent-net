package gateway

import (
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// runRow is one worker_runs row, with the columns the reads of the whole table
// use (rework tasks, calibration and track records).
type runRow struct {
	nodeID, session, task, caller, model, harness, difficulty, touches, outcome string
	ended, runSeconds                                                           int64
	cont, fix, auto, midrun                                                     int
	early                                                                       bool
}

// callerRun is the caller and end time of one run that has a caller.
type callerRun struct {
	caller string
	ended  int64
}

// runsSnapshot is a parsed copy of worker_runs: the rows in (ended_at, id)
// order, the tasks they make up, and the callers in the order they first
// appear. Nothing in it depends on the clock, so a window cut is applied by
// the reader. It is valid for one version of the table. The tasks and rows are
// shared between callers and must be treated as read only.
type runsSnapshot struct {
	version int64
	rows    []runRow
	tasks   []*taskRuns
	callers []callerRun
}

// runsSnapshot returns the copy of worker_runs for its current version. It
// reads one number per call and reloads the table only when SQLite bumped the
// version, which a trigger does on every insert, update and delete.
func (s *Store) runsSnapshot() (*runsSnapshot, error) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	// The version is read before the rows. A write between the two reads leaves
	// a copy that is newer than its version, so the next call reloads it.
	var version int64
	if err := s.db.QueryRow(`SELECT n FROM table_versions WHERE name='worker_runs'`).Scan(&version); err != nil {
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
	rows, err := s.db.Query(`SELECT node_id,session_id,task_id,caller,model,harness,difficulty,touches,outcome,ended_at,run_seconds,continue_count,fix_count,auto_continue_count,midrun_count,early_quit FROM worker_runs ORDER BY ended_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snap := &runsSnapshot{version: version}
	tasks := map[string]*taskRuns{}
	for rows.Next() {
		var r runRow
		if err := rows.Scan(&r.nodeID, &r.session, &r.task, &r.caller, &r.model, &r.harness, &r.difficulty, &r.touches, &r.outcome, &r.ended, &r.runSeconds, &r.cont, &r.fix, &r.auto, &r.midrun, &r.early); err != nil {
			return nil, err
		}
		snap.rows = append(snap.rows, r)
		if r.caller != "" {
			snap.callers = append(snap.callers, callerRun{r.caller, r.ended})
		}
		if r.session == "" {
			continue
		}
		root := r.task
		if root == "" {
			root = r.session
		}
		t := tasks[root]
		if t == nil {
			t = &taskRuns{caller: r.caller, model: r.model, harness: r.harness, difficulty: r.difficulty, sessions: map[string]bool{}, touches: map[string]bool{}}
			tasks[root] = t
			snap.tasks = append(snap.tasks, t)
		}
		if r.session == root && !t.hasRoot {
			t.caller, t.model, t.harness, t.difficulty = r.caller, r.model, r.harness, r.difficulty
			t.hasRoot = true
		}
		for _, area := range strings.Split(r.touches, ",") {
			if area != "" {
				t.touches[area] = true
			}
		}
		t.sessions[r.session] = true
		t.continueCount += r.cont
		t.fixCount += r.fix
		t.midrun += r.midrun
		t.nonStopped = t.nonStopped || r.outcome != protocol.OutcomeStopped && r.outcome != protocol.OutcomeInfra
		if !t.hasRun || r.ended > t.maxEnded {
			t.maxEnded = r.ended
		}
		t.hasRun = true
		if r.ended > t.endedAt {
			t.endedAt = r.ended
		}
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
