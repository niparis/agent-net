package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Worker worktrees: creating one, keeping it at the end, and spotting ghosts.

// createWorktree makes the worker's own worktree of its project, on a new
// branch, in a folder the daemon manages (not in the user's checkout). The
// worker then runs in it.
func (d *Daemon) createWorktree(ctx context.Context, w *worker, requestedBase ...string) error {
	main := w.path
	// A config.lock git left behind blocks `git worktree add` and the branch
	// upstream write that follows it.
	d.removeStaleConfigLock(main)
	ref := "HEAD"
	requested := len(requestedBase) > 0 && requestedBase[0] != ""
	if requested {
		name := requestedBase[0]
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("invalid base branch %q", name)
		}
		if _, err := gitOut(ctx, main, "check-ref-format", "--branch", name); err != nil {
			return fmt.Errorf("invalid base branch %q: %w", name, err)
		}
		ref = "refs/heads/" + name
	}
	base, err := gitOut(ctx, main, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		if ref != "HEAD" {
			return fmt.Errorf("base branch %q does not exist: %w", requestedBase[0], err)
		}
		return fmt.Errorf("cannot make a worktree: %s is not a git repository with a commit (%v)", main, err)
	}
	if !requested {
		base = d.freshUpstreamBase(ctx, main, base)
	}
	dir := filepath.Join(d.cfg.StateDir, "worktrees", w.id)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	branch := protocol.WorktreeBranchWithSettings(w.id, d.SettingsSnapshot())
	d.wmu.Lock()
	previousBase := w.base
	if w.branch != "" {
		branch = w.branch
	}
	d.wmu.Unlock()
	// A resumed worker's branch stays after its worktree folder is removed (a
	// disk cleanup). Make the worktree on that branch instead of failing on
	// -b, so the commits the branch holds are still there.
	reuse := false
	if _, err := gitOut(ctx, main, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}"); err == nil {
		reuse = true
	}
	args := []string{"worktree", "add", dir}
	if reuse {
		// A registration of the removed folder would block the add.
		if _, err := gitOut(ctx, main, "worktree", "prune"); err != nil {
			return fmt.Errorf("cannot make a worktree: git worktree prune: %w", err)
		}
		args = append(args, branch)
	} else {
		args = append(args, "-b", branch, base)
	}
	if _, err := gitOutWithContext(ctx, main, args...); err != nil {
		var gitErr *workerGitError
		if errors.As(err, &gitErr) {
			message := gitErr.stderr
			missingLFS := strings.Contains(message, "git-lfs: command not found")
			if len(message) > 200 {
				message = message[len(message)-200:]
			}
			if missingLFS {
				return fmt.Errorf("cannot make a worktree: git-lfs is missing on this machine: %s: %w", message, gitErr.err)
			}
			return fmt.Errorf("cannot make a worktree: git worktree add: %s: %w", message, gitErr.err)
		}
		return fmt.Errorf("cannot make a worktree: %w", err)
	}
	if err := setWorktreeUpstream(ctx, main, branch); err != nil {
		d.Logf("worker %s: branch upstream: %v", w.id, err) // the worker can still push with an explicit branch
	}
	if err := d.copyWorktreeSkills(ctx, main, dir); err != nil {
		_, _ = gitOut(ctx, main, "worktree", "remove", "--force", dir)
		return err
	}
	d.wmu.Lock()
	w.mainPath, w.path, w.worktree, w.branch = main, dir, dir, branch
	if !reuse || previousBase == "" {
		w.base = base
	}
	d.wmu.Unlock()
	return nil
}

// staleConfigLockAge is how long an empty .git/config.lock must sit before the
// daemon treats it as left behind. git holds the lock for milliseconds.
const staleConfigLockAge = 10 * time.Second

// removeStaleConfigLock removes an empty shared-repository config.lock that
// git left behind, which blocks worktree creation and branch config writes. A
// lock that holds content or is younger than staleConfigLockAge may belong to
// a running git process and is left alone.
func (d *Daemon) removeStaleConfigLock(main string) {
	path := filepath.Join(main, ".git", "config.lock")
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 || time.Since(info.ModTime()) <= staleConfigLockAge {
		return
	}
	if err := os.Remove(path); err == nil {
		d.Logf("worker.git stale config.lock removed path=%s", path)
	}
}

// finishWorktree keeps ended worktrees until the next cleanup check.
func (d *Daemon) finishWorktree(w *worker) {
	d.wmu.Lock()
	dir, branch := w.worktree, w.branch
	d.wmu.Unlock()
	if dir == "" {
		return
	}
	if d.isOwnerWorktree(dir) {
		d.Logf("worker.worktree kept owner path=%s", dir)
		return
	}
	d.Logf("worker.worktree kept worker=%s path=%s branch=%s", w.id, dir, branch)
}

// worktreesDir is the folder where this daemon creates the git worktrees of
// its workers, as a cleaned absolute path.
func (d *Daemon) worktreesDir() string {
	root, err := filepath.Abs(filepath.Join(d.cfg.StateDir, "worktrees"))
	if err != nil {
		return ""
	}
	return root
}

// dropWorktreeSessions closes the plugin connections of live sessions that
// work inside the worktrees folder and are not a worker's own. They are ghosts
// of workers that a user's own service registered before the hello refused
// them. Closing the connection takes the session offline.
func (d *Daemon) dropWorktreeSessions() {
	var drop []*localConn
	d.mu.Lock()
	for _, ls := range d.sessions {
		if ls.conn != nil && !ls.owned && ls.info.Worker == nil && d.inWorktreesDir(ls.info.Cwd) {
			drop = append(drop, ls.conn)
		}
	}
	d.mu.Unlock()
	for _, c := range drop {
		c.close()
	}
}

// inWorktreesDir tells whether cwd is inside the folder where this daemon
// creates the git worktrees of its workers. It compares cleaned absolute paths
// and works for a folder that no longer exists.
func (d *Daemon) inWorktreesDir(cwd string) bool {
	root := d.worktreesDir()
	if cwd == "" || root == "" {
		return false
	}
	p, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
