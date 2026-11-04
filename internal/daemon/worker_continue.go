package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	l := protocol.WorkerLaunch{Project: r.Project, Path: path, Harness: harness, Model: model, Owner: r.Owner, OwnerName: r.OwnerName,
		Title:       first(p.Title, r.Title),
		Permissions: r.Permissions, FromCTO: p.FromCTO, // the old worker's level, not the project's current one
		DifficultyAsked: r.DifficultyAsked, DifficultyUsed: r.DifficultyUsed, Touches: splitTouches(r.Touches),
		TaskID: first(p.TaskID, first(r.TaskID, r.SessionID)), ContinuationPartial: r.DoneStatus != "" && r.DonePartial}
	if len(p.Touches) > 0 {
		l.Touches = p.Touches
	}
	where := ""
	switch {
	case r.Worktree == "":
		l.Isolation = protocol.IsolationNone
		where = "folder"
	default:
		l.Isolation = protocol.IsolationWorktree
		// Leftover changes (a partial hand-back, or an end with
		// delivery.save_unfinished off) are committed on the old branch now,
		// whatever that setting says, so the new branch has them. Complete
		// work gets the title as its subject, other work the unfinished
		// subject. When everything is committed already this does nothing.
		subject := unfinishedSubject(r.ID, r.EndedReason)
		title := strings.TrimSpace(strings.SplitN(r.Title, "\n", 2)[0])
		complete := title != "" && r.DoneStatus == protocol.StatusDone && !r.DonePartial
		if complete {
			subject = title
		}
		old := &worker{id: r.ID, worktree: r.Worktree, branch: r.Branch, harness: r.Harness, model: r.Model, sessionID: r.SessionID,
			owner: r.Owner, ownerName: r.OwnerName, difficultyAsked: r.DifficultyAsked, difficultyUsed: r.DifficultyUsed}
		d.saveWorkerChanges(old, subject, r.EndedReason, !complete)
		// The branch is kept when the old worker left changes. If it is gone,
		// the new worker starts from the project's current commit.
		// Cleanup removes an unchanged worker branch, even when its starting
		// commit contains work inherited from an earlier worker. Restore it
		// when another worker branch points there, or its commit is outside
		// main HEAD. This also works if main has since incorporated that work.
		if r.Branch != "" && r.Base != "" {
			if _, err := gitOut(ctx, path, "rev-parse", "--verify", "refs/heads/"+r.Branch+"^{commit}"); err != nil {
				parents, err := gitOut(ctx, path, "for-each-ref", "--format=%(refname)", "--points-at", r.Base, "refs/heads/agent-net/")
				if err != nil {
					return none, fmt.Errorf("cannot find worker branches at %s: %w", r.Base, err)
				}
				_, ancestorErr := gitOut(ctx, path, "merge-base", "--is-ancestor", r.Base, "HEAD")
				if parents != "" || ancestorErr != nil {
					if _, err := gitOut(ctx, path, "branch", r.Branch, r.Base); err != nil {
						return none, fmt.Errorf("cannot restore worker branch %s: %w", r.Branch, err)
					}
				}
			}
		}
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
	// A continue keeps the judge label of the run it continues, unless the
	// caller adds work to the brief. Then the new brief is assessed once,
	// here, before the start is queued.
	if p.Task == "" && p.Assessment != nil {
		kept := *p.Assessment
		kept.RunID = protocol.NewID("k")
		kept.SessionID = ""
		kept.StartedAt = time.Now().UnixNano()
		kept.AssessedAt = time.Now().Unix()
		kept.PickerLabel = protocol.WorkerLevel(&kept)
		l.Assessment = &kept
	} else {
		l.Assessment = d.assessWorkerStart(ctx, path, task)
	}
	l.DifficultyAsked, l.DifficultyUsed = protocol.WorkerLevel(l.Assessment), protocol.WorkerLevel(l.Assessment)
	out, err := d.startWorker(ctx, l)
	if err != nil {
		return none, err
	}
	out.Task = task
	return protocol.WorkerContinued{WorkerLaunched: out, Project: r.Project, Path: path, Harness: harness, Model: model, Title: l.Title, Assessment: l.Assessment}, nil
}
