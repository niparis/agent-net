package daemon

import "time"

// workerRow is a worker as the daemon database keeps it. A row with an
// ended_reason is a worker that ended but whose end the gateway has not
// been told yet.
type workerRow struct {
	ID, Harness, Project, Path, Model, Owner string
	NativeID, SessionID                      string
	PID, PGID                                int
	StartToken                               string
	StartedAt, LastActive                    time.Time
	EndedReason                              string
	// Worktree isolation (W13): the project's main tree, the worktree, its
	// branch, and the commit it started from.
	MainPath, Worktree, Branch, Base string
	// TaskMsg is the last request from the owner; a hand-back answers it.
	TaskMsg string
	// TaskText is the short task line of that request, for status views.
	TaskText string
	// DoneStatus is the status of the last hand-back, "" if there was none.
	DoneStatus string
	// Ref is the harness's own handle for the worker (see worker.ref).
	Ref string
}

func (s *state) saveWorker(w workerRow) error {
	_, err := s.db.Exec(`INSERT INTO workers (id, harness, project, path, model, owner, native_id, session_id, pid, pgid,
		start_token, started_at, last_active, ended_reason, main_path, worktree, branch, base, task_msg, task_text, done_status, harness_ref)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET native_id=excluded.native_id, session_id=excluded.session_id,
		path=excluded.path, pid=excluded.pid, pgid=excluded.pgid, start_token=excluded.start_token,
		last_active=excluded.last_active, ended_reason=excluded.ended_reason, task_msg=excluded.task_msg,
		task_text=excluded.task_text,
		done_status=excluded.done_status, harness_ref=excluded.harness_ref`,
		w.ID, w.Harness, w.Project, w.Path, w.Model, w.Owner, w.NativeID, w.SessionID, w.PID, w.PGID,
		w.StartToken, w.StartedAt.UnixNano(), w.LastActive.UnixNano(), w.EndedReason,
		w.MainPath, w.Worktree, w.Branch, w.Base, w.TaskMsg, w.TaskText, w.DoneStatus, w.Ref)
	return err
}

func (s *state) workers() ([]workerRow, error) {
	rows, err := s.db.Query(`SELECT id, harness, project, path, model, owner, native_id, session_id, pid, pgid,
		start_token, started_at, last_active, ended_reason, main_path, worktree, branch, base, task_msg, task_text, done_status, harness_ref
		FROM workers ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workerRow
	for rows.Next() {
		var w workerRow
		var started, active int64
		if err := rows.Scan(&w.ID, &w.Harness, &w.Project, &w.Path, &w.Model, &w.Owner, &w.NativeID, &w.SessionID,
			&w.PID, &w.PGID, &w.StartToken, &started, &active, &w.EndedReason,
			&w.MainPath, &w.Worktree, &w.Branch, &w.Base, &w.TaskMsg, &w.TaskText, &w.DoneStatus, &w.Ref); err != nil {
			return nil, err
		}
		w.StartedAt, w.LastActive = time.Unix(0, started), time.Unix(0, active)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *state) deleteWorker(id string) error {
	_, err := s.db.Exec(`DELETE FROM workers WHERE id = ?`, id)
	return err
}
