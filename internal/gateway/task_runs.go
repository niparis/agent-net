package gateway

// taskRuns assembles retained runs into tasks. Root metadata always comes
// from the root session's first run, even when that run was stopped. A task
// belongs to the window when at least one of its runs ended within it, which
// is when maxEnded, the latest end time of its runs, is inside the window.
type taskRuns struct {
	caller, model, harness, difficulty string
	continueCount, fixCount, midrun    int
	endedAt, maxEnded                  int64
	nonStopped, hasRoot, hasRun        bool
	sessions                           map[string]bool
	touches                            map[string]bool
}

// reworkTasks returns the tasks that have a run in the window and at least one
// run that was not stopped, and the callers of the runs in the window in the
// order they first appear. It reads the copy of worker_runs that
// runsSnapshot keeps, so it does not scan the table. The tasks are shared:
// callers must not change them.
func (s *Store) reworkTasks(cut int64) ([]*taskRuns, []string, error) {
	snap, err := s.runsSnapshot()
	if err != nil {
		return nil, nil, err
	}
	var callers []string
	seenCaller := map[string]bool{}
	for _, c := range snap.callers {
		if c.ended >= cut && !seenCaller[c.caller] {
			seenCaller[c.caller] = true
			callers = append(callers, c.caller)
		}
	}
	out := make([]*taskRuns, 0, len(snap.tasks))
	for _, t := range snap.tasks {
		if t.maxEnded >= cut && t.nonStopped {
			out = append(out, t)
		}
	}
	return out, callers, nil
}
