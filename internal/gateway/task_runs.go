package gateway

import "github.com/niparis/agent-net/internal/protocol"

// taskRuns assembles retained runs into tasks. Root metadata always comes
// from the root session's first run, even when that run was stopped. A task
// belongs to the window when at least one of its runs ended within it.
type taskRuns struct {
	caller, model, harness, difficulty string
	continueCount, fixCount, midrun    int
	endedAt                            int64
	nonStopped, recent, hasRoot        bool
	sessions                           map[string]bool
}

func (s *Store) reworkTasks(cut int64) ([]*taskRuns, []string, error) {
	rows, err := s.db.Query(`SELECT session_id,task_id,caller,model,harness,difficulty,outcome,ended_at,continue_count,fix_count,midrun_count FROM worker_runs ORDER BY ended_at,id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	tasks := map[string]*taskRuns{}
	var order, callers []string
	seenCaller := map[string]bool{}
	for rows.Next() {
		var sid, root, caller, model, harness, difficulty, outcome string
		var ended int64
		var cont, fix, mid int
		if err := rows.Scan(&sid, &root, &caller, &model, &harness, &difficulty, &outcome, &ended, &cont, &fix, &mid); err != nil {
			return nil, nil, err
		}
		if ended >= cut && caller != "" && !seenCaller[caller] {
			seenCaller[caller] = true
			callers = append(callers, caller)
		}
		if sid == "" {
			continue
		}
		if root == "" {
			root = sid
		}
		t := tasks[root]
		if t == nil {
			t = &taskRuns{caller: caller, model: model, harness: harness, difficulty: difficulty, sessions: map[string]bool{}}
			tasks[root] = t
			order = append(order, root)
		}
		if sid == root && !t.hasRoot {
			t.caller, t.model, t.harness, t.difficulty = caller, model, harness, difficulty
			t.hasRoot = true
		}
		t.sessions[sid] = true
		t.continueCount += cont
		t.fixCount += fix
		t.midrun += mid
		t.nonStopped = t.nonStopped || outcome != protocol.OutcomeStopped
		t.recent = t.recent || ended >= cut
		if ended > t.endedAt {
			t.endedAt = ended
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	out := make([]*taskRuns, 0, len(order))
	for _, root := range order {
		if t := tasks[root]; t.recent && t.nonStopped {
			out = append(out, t)
		}
	}
	return out, callers, nil
}
