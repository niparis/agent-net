package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The one commit path for a worker's leftover changes at a hand-back, a
// continue or the end of a run.

// unfinishedSubject is the subject of the commit that keeps unfinished work.
func unfinishedSubject(workerID, reason string) string {
	return "agent-net: unfinished work of worker " + workerID + " (" + reason + ")"
}

// saveUnfinished commits lost worker changes during adoption, before any
// notice reporting can fail. A clean worktree is an idempotent no-op.
func (d *Daemon) saveUnfinished(w *worker, reason string) bool {
	if w.worktree == "" {
		return false
	}
	unlock := d.lockWorktree(w.worktree)
	defer unlock()
	if w.harness == "agy" {
		result := d.saveAgyWorkerEnd(w, reason)
		return result.SHA != ""
	}
	result := d.saveWorkerEndWIP(w, reason)
	if result.Failed != "" {
		d.Logf("worker %s: adoption WIP commit: %s", w.id, result.Failed)
	}
	return result.SHA != ""
}

type worktreeLock struct {
	mu   sync.Mutex
	refs int
}

// lockWorktree serializes git operations by path, including operations that
// rebuild a worker value from a persisted row during daemon adoption.
func (d *Daemon) lockWorktree(path string) func() {
	if path == "" {
		return func() {}
	}
	key, err := filepath.Abs(path)
	if err != nil {
		key = filepath.Clean(path)
	} else {
		key = filepath.Clean(key)
	}
	d.worktreeLocksMu.Lock()
	if d.worktreeLocks == nil {
		d.worktreeLocks = make(map[string]*worktreeLock)
	}
	lock := d.worktreeLocks[key]
	if lock == nil {
		lock = &worktreeLock{}
		d.worktreeLocks[key] = lock
	}
	lock.refs++
	d.worktreeLocksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		d.worktreeLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && d.worktreeLocks[key] == lock {
			delete(d.worktreeLocks, key)
		}
		d.worktreeLocksMu.Unlock()
	}
}

type workerEndCommitResult struct {
	SHA     string
	Files   int
	Skipped string
	Failed  string
	Legacy  bool // Agy keeps its historical title-based commit subject.
}

func workerCommitFileCount(ctx context.Context, dir, rev string) int {
	files, err := gitOut(ctx, dir, "show", "--format=", "--name-only", rev)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(files, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (r workerEndCommitResult) noticeDetail() string {
	if r.Failed != "" {
		if r.Legacy {
			return "left uncommitted changes; unfinished work commit failed: " + r.Failed
		}
		return "left uncommitted changes; WIP commit failed: " + r.Failed
	}
	if r.Skipped == "git operation in progress" {
		return ""
	}
	if r.SHA == "" {
		if r.Skipped != "" {
			return "left uncommitted changes; skipped: " + r.Skipped
		}
		return ""
	}
	kind := "WIP "
	if r.Legacy {
		kind = ""
	}
	detail := fmt.Sprintf("left uncommitted changes; committed as %s%s: %d files", kind, r.SHA, r.Files)
	if r.Skipped != "" {
		detail += "; skipped: " + r.Skipped
	}
	return detail
}

func (r workerEndCommitResult) keepWorktree() bool {
	return r.Failed != "" || r.Skipped != ""
}

// saveWorkerEndWIP saves the uncommitted changes of a run that ended without a
// complete hand-back. It is saveWorkerChanges with the WIP subject.
func (d *Daemon) saveWorkerEndWIP(w *worker, reason string) workerEndCommitResult {
	return d.saveWorkerChanges(w, "WIP: uncommitted changes at worker end ("+reason+")", reason, false)
}

// saveAgyWorkerEnd preserves Antigravity's historical title-based unfinished
// commit while running it in the worker-end job. It uses the one commit path.
func (d *Daemon) saveAgyWorkerEnd(w *worker, reason string) workerEndCommitResult {
	d.wmu.Lock()
	subject := strings.TrimSpace(strings.SplitN(w.title, "\n", 2)[0])
	d.wmu.Unlock()
	if subject == "" {
		subject = unfinishedSubject(w.id, reason)
	}
	result := d.saveWorkerChanges(w, subject, reason, true)
	if result.SHA == "" && result.Failed == "" && result.Skipped == "" {
		return workerEndCommitResult{}
	}
	result.Legacy = true
	if result.SHA == "" && result.Failed == "" && result.Skipped != "git operation in progress" {
		result.Failed = "could not commit Antigravity worker changes"
	}
	return result
}

// saveWorkerChanges commits the worker's leftover changes on its branch, and
// returns what it did for the notice. It is the one commit path for a
// hand-back, a continue and the end of a run, for every harness.
func (d *Daemon) saveWorkerChanges(w *worker, subject, reason string, unfinished bool) workerEndCommitResult {
	d.wmu.Lock()
	dir, id, branch := w.worktree, w.id, w.branch
	d.wmu.Unlock()
	if dir == "" {
		return workerEndCommitResult{}
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return workerEndCommitResult{}
	}
	ctx := context.Background()
	if branch != "" {
		if current, err := gitOut(ctx, dir, "branch", "--show-current"); err != nil || current != branch {
			d.Logf("worker %s: cannot save changes: worktree is not on worker branch", id)
			return workerEndCommitResult{Failed: "worktree is not on worker branch"}
		}
	}
	dirty, err := hasWorkLeftovers(ctx, dir)
	if err != nil {
		return workerEndCommitResult{Failed: err.Error()}
	}
	if !dirty {
		return workerEndCommitResult{}
	}
	// A commit now would drop the merge parents (or the rest of a rebase), so
	// the worktree stays as it is. It is kept because it has changes.
	if gitOperationInProgress(ctx, dir) {
		d.wmu.Lock()
		w.leftAsIs = true
		d.wmu.Unlock()
		d.Logf("worker.unfinished_left worker=%s reason=%s path=%s (a git merge, rebase, cherry-pick or revert is in progress)", id, reason, dir)
		return workerEndCommitResult{Skipped: "git operation in progress"}
	}
	excluded, err := stageWorkerLeftovers(ctx, dir)
	d.wmu.Lock()
	w.excludedLeftovers = excluded
	d.wmu.Unlock()
	if err != nil {
		d.Logf("worker %s: keep unfinished work: %v", id, err)
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	diff, err := gitOut(ctx, dir, "diff", "--cached", "--name-only")
	if err != nil {
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	if diff == "" {
		return workerEndCommitResult{Skipped: excluded}
	}
	args := []string{"-c", "commit.gpgsign=false"}
	if v, _ := gitOut(ctx, dir, "config", "user.name"); v == "" {
		args = append(args, "-c", "user.name=agent-net")
	}
	if v, _ := gitOut(ctx, dir, "config", "user.email"); v == "" {
		args = append(args, "-c", "user.email=agent-net@localhost")
	}
	args = append(args, "commit", "-q", "--no-verify", "-m", subject)
	if _, err := gitOutWithEnv(ctx, dir, d.workerGitEnv(w, nil, nil), args...); err != nil {
		d.Logf("worker %s: keep unfinished work: %v", id, err)
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	if unfinished {
		d.wmu.Lock()
		w.unfinished = true
		d.wmu.Unlock()
	}
	sha, err := gitOut(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return workerEndCommitResult{Skipped: excluded, Failed: err.Error()}
	}
	d.Logf("worker.unfinished_saved worker=%s reason=%s path=%s", id, reason, dir)
	return workerEndCommitResult{SHA: sha, Files: workerCommitFileCount(ctx, dir, "HEAD"), Skipped: excluded}
}
