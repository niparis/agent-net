package daemon

import (
	"context"
	"strings"

	"github.com/niparis/agent-net/internal/settings"
)

// A worker can be based on another worker's branch. It then has its own
// worktree at that branch's head, but a worker that goes to the other worker's
// worktree by mistake commits there instead: its own branch stays empty and
// its change looks empty to the caller. The commits still carry its trailer,
// so the hand-back can say where they went.

// misplacedWorkerCommit returns the branch of another worker that holds the
// commits this worker made in the wrong worktree, or "" when there is none.
// It only looks when the worker's own branch has no commit after its base.
func (d *Daemon) misplacedWorkerCommit(w *worker) string {
	d.wmu.Lock()
	dir, branch, base, session := w.worktree, w.branch, w.base, w.sessionID
	d.wmu.Unlock()
	if dir == "" || branch == "" || base == "" || session == "" {
		return ""
	}
	ctx := context.Background()
	if n, err := gitOut(ctx, dir, "rev-list", "--count", base+"..HEAD"); err != nil || strings.TrimSpace(n) != "0" {
		return ""
	}
	snap := d.SettingsSnapshot()
	prefix := snap.String("delivery.worker_branch_prefix", settings.Where{})
	trailer := strings.TrimSpace(snap.String("delivery.trailer_worker", settings.Where{}))
	if prefix == "" || trailer == "" {
		return ""
	}
	refs, err := gitOut(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+prefix+"*")
	if err != nil {
		return ""
	}
	for _, name := range strings.Split(refs, "\n") {
		name = strings.TrimSpace(name)
		if name == "" || name == branch {
			continue
		}
		if _, err := gitOut(ctx, dir, "merge-base", "--is-ancestor", base, name); err != nil {
			continue // not based on the commit this worker started from
		}
		out, err := gitOut(ctx, dir, "log", "--format=%H", "--fixed-strings",
			"--grep="+trailer+": "+session, base+".."+name)
		if err != nil || strings.TrimSpace(out) == "" {
			continue
		}
		return name
	}
	return ""
}

// misplacedCommitNote is the hand-back notice line for a worker that committed
// on another worker's branch instead of its own, or "".
func (d *Daemon) misplacedCommitNote(w *worker) string {
	other := d.misplacedWorkerCommit(w)
	if other == "" {
		return ""
	}
	d.wmu.Lock()
	branch := w.branch
	d.wmu.Unlock()
	return "It committed on " + other + " instead of its own branch " + branch + ": its own branch has no new commits."
}
