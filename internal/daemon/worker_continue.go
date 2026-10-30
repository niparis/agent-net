package daemon

import (
	"context"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// continueWorker handles "worker.continue" from the gateway: it starts a new
// worker from an ended one of this machine. The new worker has the same
// project, harness and model unless the caller says otherwise, a new worktree
// on a new branch made from the old worker's branch, and the old worker's full
// first task. Only the owner named in the old worker's record may ask.
func (d *Daemon) continueWorker(ctx context.Context, p protocol.WorkerContinue) (protocol.WorkerContinued, error) {
	var none protocol.WorkerContinued
	rows, err := d.st.workers()
	if err != nil {
		return none, err
	}
	var r *workerRow
	for i := range rows {
		if rows[i].SessionID == p.Session {
			r = &rows[i]
		}
	}
	if r == nil {
		return none, fmt.Errorf("this machine has no record of worker %s any more", p.Session)
	}
	if r.Owner != p.Requester {
		return none, fmt.Errorf("worker %s ended, and only the session that started it can continue it", p.Session)
	}
	d.wmu.Lock()
	running := d.workers[r.ID] != nil
	d.wmu.Unlock()
	if running || r.EndedReason == "" {
		return none, fmt.Errorf("worker %s is still running: wait for it to end, or stop it first", p.Session)
	}
	if r.TaskFull == "" {
		return none, fmt.Errorf("the record of worker %s has no task (it was made by an older agent-net)", p.Session)
	}
	path := first(r.MainPath, r.Path)
	harness, model := r.Harness, r.Model
	if p.Harness != "" && p.Harness != r.Harness {
		harness, model = p.Harness, ""
	}
	if p.Model != "" {
		model = p.Model
	}
	l := protocol.WorkerLaunch{Project: r.Project, Path: path, Harness: harness, Model: model, Owner: r.Owner}
	where := ""
	switch {
	case r.Worktree == "":
		l.Isolation = protocol.IsolationNone
		where = "folder"
	default:
		l.Isolation = protocol.IsolationWorktree
		// A worker that handed back keeps its changes uncommitted: commit them
		// now, so the new branch has them. For any other ended worker this is
		// a no-op, the changes are already committed.
		d.saveUnfinished(&worker{id: r.ID, worktree: r.Worktree}, r.EndedReason)
		// The branch is kept when the old worker left changes. If it is gone,
		// the new worker starts from the project's current commit.
		if _, err := gitOut(ctx, path, "rev-parse", "--verify", "refs/heads/"+r.Branch+"^{commit}"); err == nil {
			l.Base = r.Branch
			where = "branch"
		}
	}
	record := r.TaskFull
	task := protocol.ContinueIntro(r.EndedReason, where) + "\n\n" + r.TaskFull
	if p.Task != "" {
		record += "\n\nAlso: " + p.Task
		task += "\n\nAlso: " + p.Task
	}
	l.Task, l.RecordTask = task, record
	out, err := d.startWorker(ctx, l)
	if err != nil {
		return none, err
	}
	out.Task = task
	return protocol.WorkerContinued{WorkerLaunched: out, Project: r.Project, Path: path, Harness: harness, Model: model}, nil
}
