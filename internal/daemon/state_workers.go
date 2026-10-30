package daemon

import (
	"database/sql"
	"errors"
	"time"
)

// workerRow is a worker as the daemon database keeps it. A row with an
// ended_reason is a worker that ended. It is kept until the gateway has been
// told (reported) and it is past the longest run time: a hand-back row is also
// what starts its harness session again (resumable).
type workerRow struct {
	ID, Harness, Project, Path, Model, Owner string
	NativeID, SessionID                      string
	PID, PGID                                int
	StartToken                               string
	StartedAt, LastActive                    time.Time
	EndedReason                              string
	// Resumable is true for a worker that ended after a hand-back: a message
	// from its owner starts its harness session again.
	Resumable bool
	// Reported is true when the gateway has been told that the worker ended.
	Reported bool
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
	// TaskFull is the full first task of the worker (at most 64 KiB), so that
	// a new worker can continue it.
	TaskFull string
}

func (s *state) saveWorker(w workerRow) error {
	_, err := s.db.Exec(`INSERT INTO workers (id, harness, project, path, model, owner, native_id, session_id, pid, pgid,
		start_token, started_at, last_active, ended_reason, resumable, reported, main_path, worktree, branch, base,
		task_msg, task_text, done_status, harness_ref, task_full)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET native_id=excluded.native_id, session_id=excluded.session_id,
		path=excluded.path, pid=excluded.pid, pgid=excluded.pgid, start_token=excluded.start_token,
		last_active=excluded.last_active, ended_reason=excluded.ended_reason, resumable=excluded.resumable,
		reported=excluded.reported, task_msg=excluded.task_msg, task_text=excluded.task_text,
		done_status=excluded.done_status, harness_ref=excluded.harness_ref, task_full=excluded.task_full`,
		w.ID, w.Harness, w.Project, w.Path, w.Model, w.Owner, w.NativeID, w.SessionID, w.PID, w.PGID,
		w.StartToken, w.StartedAt.UnixNano(), w.LastActive.UnixNano(), w.EndedReason, boolInt(w.Resumable),
		boolInt(w.Reported), w.MainPath, w.Worktree, w.Branch, w.Base, w.TaskMsg, w.TaskText, w.DoneStatus, w.Ref, w.TaskFull)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *state) workers() ([]workerRow, error) {
	rows, err := s.db.Query(`SELECT id, harness, project, path, model, owner, native_id, session_id, pid, pgid,
		start_token, started_at, last_active, ended_reason, resumable, reported, main_path, worktree, branch, base,
		task_msg, task_text, done_status, harness_ref, task_full
		FROM workers ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workerRow
	for rows.Next() {
		var w workerRow
		var started, active int64
		var resumable, reported int
		if err := rows.Scan(&w.ID, &w.Harness, &w.Project, &w.Path, &w.Model, &w.Owner, &w.NativeID, &w.SessionID,
			&w.PID, &w.PGID, &w.StartToken, &started, &active, &w.EndedReason, &resumable, &reported,
			&w.MainPath, &w.Worktree, &w.Branch, &w.Base, &w.TaskMsg, &w.TaskText, &w.DoneStatus, &w.Ref, &w.TaskFull); err != nil {
			return nil, err
		}
		w.StartedAt, w.LastActive = time.Unix(0, started), time.Unix(0, active)
		w.Resumable, w.Reported = resumable == 1, reported == 1
		out = append(out, w)
	}
	return out, rows.Err()
}

// workerBySession returns the worker row of a session, if there is one.
func (s *state) workerBySession(sessionID string) (workerRow, bool, error) {
	var w workerRow
	var started, active int64
	var resumable, reported int
	err := s.db.QueryRow(`SELECT id, harness, project, path, model, owner, native_id, session_id, pid, pgid,
		start_token, started_at, last_active, ended_reason, resumable, reported, main_path, worktree, branch, base,
		task_msg, task_text, done_status, harness_ref, task_full
		FROM workers WHERE session_id = ?`, sessionID).
		Scan(&w.ID, &w.Harness, &w.Project, &w.Path, &w.Model, &w.Owner, &w.NativeID, &w.SessionID,
			&w.PID, &w.PGID, &w.StartToken, &started, &active, &w.EndedReason, &resumable, &reported,
			&w.MainPath, &w.Worktree, &w.Branch, &w.Base, &w.TaskMsg, &w.TaskText, &w.DoneStatus, &w.Ref, &w.TaskFull)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return w, false, nil
		}
		return w, false, err
	}
	w.StartedAt, w.LastActive = time.Unix(0, started), time.Unix(0, active)
	w.Resumable, w.Reported = resumable == 1, reported == 1
	return w, true, nil
}

// workerNativeKnown tells whether a worker record names this harness session,
// whether the worker runs now or ended. The record is kept after a hand-back so
// the owner can start the session again; it also says that the session is a
// worker's, so the plugin of another server may not register it (W19).
func (s *state) workerNativeKnown(harness, native string) bool {
	if native == "" {
		return false
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE harness = ? AND native_id = ?`, harness, native).Scan(&n)
	return n > 0
}

func (s *state) deleteWorker(id string) error {
	_, err := s.db.Exec(`DELETE FROM workers WHERE id = ?`, id)
	return err
}

// markWorkerReported records that the gateway has been told that an ended
// worker ended. It changes nothing when the worker has been started again.
func (s *state) markWorkerReported(id, reason string) error {
	_, err := s.db.Exec(`UPDATE workers SET reported = 1 WHERE id = ? AND ended_reason = ?`, id, reason)
	return err
}
