package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Cleanup uses the network schedule, read at daemon startup.
func (d *Daemon) cleanupLoop(ctx context.Context) {
	timer := time.NewTimer(d.SettingsSnapshot().Duration("delivery.cleanup_first_delay", settings.Where{}))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			d.cleanupProjects(ctx)
			timer.Reset(d.SettingsSnapshot().Duration("delivery.cleanup_interval", settings.Where{}))
		}
	}
}

// runEvery calls fn after first, then every interval, until ctx ends.
func runEvery(ctx context.Context, first, every time.Duration, fn func()) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
			t.Reset(every)
		}
	}
}

func (d *Daemon) cleanupNow() time.Time {
	if d.testCleanupNow != nil {
		return d.testCleanupNow()
	}
	if d.cfg.testCleanupNow != nil {
		return d.cfg.testCleanupNow()
	}
	return time.Now()
}

func checkProjectWorkTree(ctx context.Context, projectPath string) error {
	fi, err := os.Stat(projectPath)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("not a directory: %s", projectPath)
	}
	out, err := gitOut(ctx, projectPath, "rev-parse", "--is-inside-work-tree")
	var ge *workerGitError
	if errors.As(err, &ge) && strings.Contains(ge.stderr, "not a git repository") {
		return errNotGitRepo
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "true" {
		return errNotGitRepo
	}
	return nil
}

// errNotGitRepo marks a registered folder that is not a git repository, such
// as a home folder. Cleanup has nothing to do there, so it is not a failure.
var errNotGitRepo = errors.New("not a git repository")

func (d *Daemon) shouldLogProjectCleanup(project string, now time.Time) bool {
	d.cleanupLogMu.Lock()
	defer d.cleanupLogMu.Unlock()
	if d.cleanupProjectLogs == nil {
		d.cleanupProjectLogs = make(map[string]time.Time)
	}
	last, ok := d.cleanupProjectLogs[project]
	if !ok || now.Sub(last) >= 24*time.Hour {
		d.cleanupProjectLogs[project] = now
		return true
	}
	return false
}

func (d *Daemon) shouldLogWorktreeError(path, errText string, now time.Time) bool {
	d.cleanupLogMu.Lock()
	defer d.cleanupLogMu.Unlock()
	if d.cleanupWorktreeLogs == nil {
		d.cleanupWorktreeLogs = make(map[worktreeErrKey]time.Time)
	}
	key := worktreeErrKey{path: filepath.Clean(path), err: errText}
	last, ok := d.cleanupWorktreeLogs[key]
	if !ok || now.Sub(last) >= 24*time.Hour {
		d.cleanupWorktreeLogs[key] = now
		return true
	}
	return false
}

func (d *Daemon) cleanupProjects(ctx context.Context) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := d.call(queryCtx, "query.projects", struct{}{})
	if err != nil {
		return
	}
	var result protocol.ProjectsResult
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	for _, p := range result.Projects {
		if p.NodeID != d.cfg.NodeID {
			continue
		}
		now := d.cleanupNow()
		if projErr := checkProjectWorkTree(ctx, p.Path); projErr != nil {
			if errors.Is(projErr, errNotGitRepo) {
				continue
			}
			if d.log != nil && d.shouldLogProjectCleanup(p.Path, now) {
				d.Logf("worker.cleanup project=%q error=%v", p.Path, projErr)
			}
			continue
		}
		if err := d.observeLegacyWorktrees(ctx, p.Path, now); err != nil {
			if d.log != nil && d.shouldLogProjectCleanup(p.Path, now) {
				d.Logf("worker.cleanup project=%q error=%v", p.Path, err)
			}
			continue
		}
		rows, err := d.st.cleanupWorktrees(p.Path)
		if err != nil {
			continue
		}
		defaultBranch := cleanupDefaultBranch(ctx, p.Path, p.DefaultBranch)
		// Refresh the project's default branch once, before any row work and
		// outside every cleanup lock.
		remoteRef := ""
		if len(rows) > 0 && defaultBranch != "" {
			fetch := d.cfg.testCleanupFetch
			if fetch == nil {
				fetch = cleanupFetchDefault
			}
			remoteRef = fetch(ctx, p.Path, defaultBranch)
		}
		ready := make([]cleanupWorktree, 0, len(rows))
		for _, row := range rows {
			row.DefaultBranch = defaultBranch
			row.RemoteDefaultRef = remoteRef
			if err := d.cleanupChange(ctx, p.Name, &row); err != nil {
				if d.log != nil {
					d.Logf("worker.worktree_cleanup path=%q change_error=%v", row.Path, err)
				}
				continue
			}
			ready = append(ready, row)
		}
		for i := range ready {
			if err := cleanupPrepareProof(ctx, &ready[i]); err != nil {
				if d.log != nil {
					d.Logf("worker.worktree_cleanup path=%q prepare_error=%v", ready[i].Path, err)
				}
				continue
			}
			if err := d.cleanupPrepareContained(ctx, p.Name, &ready[i]); err != nil {
				if d.log != nil {
					d.Logf("worker.worktree_cleanup path=%q prepare_error=%v", ready[i].Path, err)
				}
				continue
			}
			if err := d.cleanupWorktree(ctx, ready[i], now); err != nil && d.log != nil {
				if d.shouldLogWorktreeError(ready[i].Path, err.Error(), now) {
					d.Logf("worker.worktree_cleanup path=%q error=%v", ready[i].Path, err)
				}
			}
		}
	}
}

type worktreeErrKey struct {
	path, err string
}

type cleanupWorktree struct {
	ID, Project, Path, Branch, Harness, DefaultBranch  string
	EndedAt, ChangeEndedAt, RetainUntil, DirtyLoggedAt time.Time
	ChangeState, MergedCommit, RetainHead              string
	ChangeChecked                                      bool
	// RemoteDefaultRef is the fetched origin/<default> ref for the project,
	// resolved once per cleanup pass outside the cleanup locks.
	RemoteDefaultRef string
	// MergedReason and MergedHead are the merged-elsewhere proof computed
	// outside the cleanup locks. Cleanup uses the proof only when the branch
	// head still equals MergedHead.
	MergedReason, MergedHead string
	// ContainedIn and ContainedHead are the proof, computed outside the cleanup
	// locks, that every commit of a closed change's branch is also on the
	// branch of another open or merged change. Cleanup uses it only when the
	// branch head still equals ContainedHead.
	ContainedIn, ContainedHead string
}

// cleanupBranch verifies a recorded worker branch without depending on the
// currently configured prefix, which may have changed after the run.
func cleanupBranch(branch, id string) bool {
	if branch == protocol.WorktreeBranch(id) {
		return true
	}
	if !strings.HasSuffix(branch, "/"+id) {
		return false
	}
	prefix := strings.TrimSuffix(branch, id)
	def, ok := settings.DefinitionFor("delivery.worker_branch_prefix")
	if !ok {
		return false
	}
	raw, _ := json.Marshal(prefix)
	_, err := def.Codec(raw)
	return err == nil
}

// cleanupClaim reads the state that decides whether a worktree may go, under
// the queue and worker locks and with no git command. It is false when a worker
// of that ID runs, a start or queue entry uses the branch, or the row changed.
// With claim set it also marks the worker as being cleaned up, so a resume
// waits for the next pass instead of using a worktree that is going away.
func (d *Daemon) cleanupClaim(w cleanupWorktree, claim bool) (bool, error) {
	d.qmu.Lock()
	defer d.qmu.Unlock()
	if d.startingBases[w.Branch] > 0 {
		return false, nil
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if _, ok := d.workers[w.ID]; ok {
		return false, nil
	}
	if claim && d.cleaningWorkers[w.ID] {
		return false, nil
	}
	if d.st != nil {
		var active int
		if err := d.st.db.QueryRow(`SELECT count(*) FROM workers WHERE id=? AND ended_reason=''`, w.ID).Scan(&active); err != nil {
			return false, err
		}
		if active != 0 {
			return false, nil
		}
		queued, err := d.st.queue()
		if err != nil {
			return false, err
		}
		for _, q := range queued {
			if q.Base == w.Branch || filepath.Clean(q.Path) == filepath.Clean(w.Path) {
				return false, nil
			}
		}
		var current int
		if err := d.st.db.QueryRow(`SELECT count(*) FROM cleanup_worktrees WHERE worker_id=? AND project=? AND path=? AND branch=? AND ended_at=?`, w.ID, w.Project, w.Path, w.Branch, w.EndedAt.UnixNano()).Scan(&current); err != nil {
			return false, err
		}
		if current == 0 {
			return false, nil
		}
	}
	if d.isOwnerWorktree(w.Path) {
		return false, nil
	}
	if claim {
		if d.cleaningWorkers == nil {
			d.cleaningWorkers = make(map[string]bool)
		}
		d.cleaningWorkers[w.ID] = true
	}
	return true, nil
}

func (d *Daemon) cleanupRelease(id string) {
	d.wmu.Lock()
	delete(d.cleaningWorkers, id)
	d.wmu.Unlock()
}

// cleanupWorktree never removes a worktree with uncommitted files or a branch
// containing commits absent from the project default branch.
func (d *Daemon) cleanupWorktree(ctx context.Context, w cleanupWorktree, now time.Time) error {
	if w.EndedAt.IsZero() {
		return nil
	}
	if !strings.HasPrefix(w.ID, "w_") || filepath.Base(w.ID) != w.ID || !cleanupBranch(w.Branch, w.ID) || filepath.Clean(w.Path) != filepath.Join(d.worktreesDir(), w.ID) {
		return nil
	}
	// The state is read under the locks and the claim is taken there. The git
	// commands below run without them: a slow git (75 worktrees on a busy
	// machine) held up every resume and start. The claim keeps a resume of this
	// worker out, and the state is read again before anything is removed.
	if ok, err := d.cleanupClaim(w, true); err != nil || !ok {
		return err
	}
	defer d.cleanupRelease(w.ID)
	info, err := os.Lstat(w.Path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return err
	}
	branchRef := "refs/heads/" + w.Branch
	worktrees, err := gitOut(ctx, w.Project, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	checkedOutCount := 0
	for _, field := range strings.Split(worktrees, "\x00") {
		if field == "branch "+branchRef {
			checkedOutCount++
		}
	}
	if (missing && checkedOutCount != 0) || (!missing && checkedOutCount != 1) {
		return nil
	}
	if !missing {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		checkedOut, err := gitOut(ctx, w.Path, "symbolic-ref", "-q", "HEAD")
		if err != nil || checkedOut != branchRef {
			return nil
		}
		status, err := gitOut(ctx, w.Path, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			return err
		}
		if status != "" {
			if d.log != nil && (w.DirtyLoggedAt.IsZero() || now.Sub(w.DirtyLoggedAt) >= 24*time.Hour) {
				d.Logf("worker.worktree_cleanup path=%q dirty=true", w.Path)
				if d.st != nil {
					_, err = d.st.db.Exec(`UPDATE cleanup_worktrees SET dirty_logged_at=? WHERE worker_id=? AND ended_at=?`, now.UnixNano(), w.ID, w.EndedAt.UnixNano())
					if err != nil {
						return err
					}
				}
			}
			return nil
		}
	}

	refs, err := gitOut(ctx, w.Project, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return err
	}
	found := false
	for _, ref := range strings.Split(refs, "\n") {
		if ref == branchRef {
			found = true
			break
		}
	}
	if !found {
		if missing && d.st != nil {
			_, err = d.st.db.Exec(`DELETE FROM cleanup_worktrees WHERE worker_id=? AND ended_at=?`, w.ID, w.EndedAt.UnixNano())
			return err
		}
		return nil
	}
	head, err := gitOut(ctx, w.Project, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return err
	}
	if w.RetainHead != "" && head != w.RetainHead {
		return nil
	}
	defaultBranch := w.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = cleanupDefaultBranch(ctx, w.Project, "")
	}
	defaultRef := "refs/heads/" + defaultBranch
	if defaultBranch == "" || defaultRef == branchRef {
		return nil
	}
	if _, err := gitOut(ctx, w.Project, "show-ref", "--verify", "--quiet", defaultRef); err != nil {
		return nil
	}
	unique, err := gitOut(ctx, w.Project, "rev-list", "--max-count=1", head, "--not", defaultRef)
	if err != nil {
		return err
	}
	reason := "merged"
	// closedSafe marks a closed change whose commits are all kept elsewhere, so
	// its branch can go with its worktree.
	closedSafe := false
	if w.ChangeState != "" && w.ChangeState != protocol.ChangeMerged && w.ChangeState != protocol.ChangeDone && w.ChangeState != protocol.ChangeStopped {
		return nil
	}
	retainUntil := w.RetainUntil
	if w.ChangeState != "" {
		switch w.ChangeState {
		case protocol.ChangeMerged, protocol.ChangeDone:
			if w.MergedCommit == "" || w.ChangeEndedAt.IsZero() {
				return nil
			}
			// A squash lands on the default branch as one normal commit, so
			// this ancestry check covers both a merge and a squash.
			if _, err := gitOut(ctx, w.Project, "merge-base", "--is-ancestor", w.MergedCommit, defaultRef); err != nil {
				return nil
			}
			// Disregard deadlines saved by an older cleanup pass.
			retainUntil = time.Time{}
		case protocol.ChangeStopped:
			if w.ChangeEndedAt.IsZero() {
				return nil
			}
			switch {
			case unique == "":
				// A closed change whose work reached the default branch another
				// way (the merger lands a train, a later change supersedes it)
				// holds nothing: free it like a merged one.
				reason, closedSafe, retainUntil = "closed-merged", true, time.Time{}
			case w.MergedReason != "" && w.MergedHead == head:
				reason, closedSafe, retainUntil = "closed-"+w.MergedReason, true, time.Time{}
			case w.ContainedIn != "" && w.ContainedHead == head && now.Sub(w.ChangeEndedAt) > 3*24*time.Hour:
				// Every commit is on the branch of another open or merged
				// change, so deleting this branch loses nothing.
				reason, closedSafe, retainUntil = "closed-contained", true, time.Time{}
			default:
				if now.Sub(w.ChangeEndedAt) <= 3*24*time.Hour {
					return nil
				}
				reason = "closed"
				if deadline := w.ChangeEndedAt.Add(30 * 24 * time.Hour); deadline.After(retainUntil) {
					retainUntil = deadline
				}
			}
		default:
			return nil
		}
	} else if retainUntil.IsZero() && unique != "" {
		// The merged-elsewhere proof was computed outside the cleanup locks.
		// Use it only for the branch head it was made for; a branch that moved
		// waits for the next pass.
		if w.MergedHead == "" || head != w.MergedHead {
			return nil
		}
		if w.MergedReason != "" {
			reason = w.MergedReason
		} else {
			if !w.ChangeChecked || now.Sub(w.EndedAt) <= 14*24*time.Hour {
				return nil
			}
			// A worker record or symbolic ref may still name this branch.
			if d.st != nil {
				var references int
				if err := d.st.db.QueryRow(`SELECT count(*) FROM workers WHERE id<>? AND base=?`, w.ID, w.Branch).Scan(&references); err != nil {
					return err
				}
				if references != 0 {
					return nil
				}
			}
			symrefs, err := gitOut(ctx, w.Project, "for-each-ref", "--format=%(symref)")
			if err != nil {
				return err
			}
			for _, ref := range strings.Split(symrefs, "\n") {
				if ref == branchRef {
					return nil
				}
			}
			// Keep commits referenced by another branch or remote.
			others, err := gitOut(ctx, w.Project, "for-each-ref", "--format=%(refname)", "--contains", head, "refs/heads/", "refs/remotes/")
			if err != nil {
				return err
			}
			for _, ref := range strings.Split(others, "\n") {
				if ref != "" && ref != branchRef {
					return nil
				}
			}
			reason = "stale"
		}
	}
	if !retainUntil.IsZero() && d.st != nil {
		if _, err := d.st.db.Exec(`UPDATE cleanup_worktrees SET retain_until=?,retain_head=? WHERE worker_id=? AND ended_at=?`, retainUntil.UnixNano(), head, w.ID, w.EndedAt.UnixNano()); err != nil {
			return err
		}
	}
	var size int64
	_ = filepath.WalkDir(w.Path, func(_ string, e os.DirEntry, err error) error {
		if err == nil && e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	// The git reads above ran with no lock. Read the state again before the
	// first removal: a worker may have been registered or a start queued.
	if ok, err := d.cleanupClaim(w, false); err != nil || !ok {
		return err
	}
	if !missing {
		if _, err := gitOut(ctx, w.Project, "worktree", "remove", w.Path); err != nil {
			unlocked := false
			reason, locked := worktreeLockReason(ctx, w.Project, w.Path)
			if locked && reason == "initializing" {
				lockFile := findWorktreeLockFile(ctx, w.Project, w.Path, w.ID)
				if lockFile != "" {
					if fi, statErr := os.Stat(lockFile); statErr == nil && now.Sub(fi.ModTime()) >= time.Hour {
						if _, unlockErr := gitOut(ctx, w.Project, "worktree", "unlock", w.Path); unlockErr != nil {
							return unlockErr
						}
						if d.log != nil {
							d.Logf("worker.worktree_cleanup path=%q unlocked_stale=initializing", w.Path)
						}
						if _, remErr := gitOut(ctx, w.Project, "worktree", "remove", w.Path); remErr != nil {
							return remErr
						}
						unlocked = true
					}
				}
			}
			if !unlocked {
				return err
			}
		}
	}
	if !missing && w.Harness == "claude" {
		if err := claudetrust.Remove(w.Path); err != nil && d.log != nil {
			d.Logf("worker.worktree_cleanup path=%q trust_error=%v", w.Path, err)
		}
	}
	if !missing && d.log != nil {
		d.Logf("worker.worktree_cleanup path=%q branch=%q reason=%s freed=%d", w.Path, w.Branch, reason, size)
	}
	if (w.ChangeState == protocol.ChangeStopped && !closedSafe) || (!retainUntil.IsZero() && now.Before(retainUntil)) {
		return nil
	}
	// A start may have taken this branch as its base while the worktree went.
	if ok, err := d.cleanupClaim(w, false); err != nil || !ok {
		return err
	}
	if _, err := gitOut(ctx, w.Project, "update-ref", "-d", branchRef, head); err != nil {
		return err
	}
	if d.st != nil {
		if _, err := d.st.db.Exec(`DELETE FROM cleanup_worktrees WHERE worker_id=? AND ended_at=?`, w.ID, w.EndedAt.UnixNano()); err != nil {
			return err
		}
	}
	return nil
}

// worktreeLockReason reads the lock reason for targetPath from git worktree list.
func worktreeLockReason(ctx context.Context, project, targetPath string) (reason string, locked bool) {
	out, err := gitOut(ctx, project, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	targetClean := filepath.Clean(targetPath)
	targetReal, errReal := filepath.EvalSymlinks(targetClean)
	inTarget := false
	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "worktree ") {
			wtPath := filepath.Clean(strings.TrimPrefix(line, "worktree "))
			inTarget = (wtPath == targetClean)
			if !inTarget && errReal == nil {
				if wtReal, errWt := filepath.EvalSymlinks(wtPath); errWt == nil && wtReal == targetReal {
					inTarget = true
				}
			}
			continue
		}
		if !inTarget {
			continue
		}
		if line == "" {
			inTarget = false
			continue
		}
		if line == "locked" {
			return "", true
		}
		if strings.HasPrefix(line, "locked ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "locked ")), true
		}
	}
	return "", false
}

// findWorktreeLockFile locates the lock file ($GIT_DIR/worktrees/<id>/locked) for a worktree.
func findWorktreeLockFile(ctx context.Context, project, wtPath, id string) string {
	if out, err := gitOut(ctx, wtPath, "rev-parse", "--git-path", "locked"); err == nil {
		p := strings.TrimSpace(out)
		if p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(wtPath, p)
			}
			return p
		}
	}
	if b, err := os.ReadFile(filepath.Join(wtPath, ".git")); err == nil {
		line := strings.TrimSpace(string(b))
		if strings.HasPrefix(line, "gitdir:") {
			gd := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if !filepath.IsAbs(gd) {
				gd = filepath.Join(wtPath, gd)
			}
			return filepath.Join(gd, "locked")
		}
	}
	if id != "" {
		if out, err := gitOut(ctx, project, "rev-parse", "--git-path", filepath.Join("worktrees", id, "locked")); err == nil {
			p := strings.TrimSpace(out)
			if p != "" {
				if !filepath.IsAbs(p) {
					p = filepath.Join(project, p)
				}
				return p
			}
		}
	}
	return ""
}

// cleanupDefaultBranch returns the project's default branch: the registered
// one, origin/HEAD, or a local main or master.
func cleanupDefaultBranch(ctx context.Context, dir, configured string) string {
	if configured != "" {
		return configured
	}
	if ref, err := gitOut(ctx, dir, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil && strings.HasPrefix(ref, "refs/remotes/origin/") {
		return strings.TrimPrefix(ref, "refs/remotes/origin/")
	}
	for _, name := range []string{"main", "master"} {
		if _, err := gitOut(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return ""
}

// cleanupWantsProof reports whether cleanup may need a merged-elsewhere proof
// for the row: a branch with no delivery change that is not already waiting on
// a retention deadline, or a branch whose change was closed. A closed change
// can hold a branch that landed on the default branch under another change.
func cleanupWantsProof(w *cleanupWorktree) bool {
	if w.DefaultBranch == "" {
		return false
	}
	switch w.ChangeState {
	case "":
		return w.RetainUntil.IsZero()
	case protocol.ChangeStopped:
		return true
	}
	return false
}

// cleanupNeedsRemoteProof reports whether a row has no delivery change (or a
// closed one) and its branch head is not on the local default branch, so cleanup must look at the
// remote and at cherry or squash proofs. It runs outside the cleanup locks.
func cleanupNeedsRemoteProof(ctx context.Context, w *cleanupWorktree) bool {
	if !cleanupWantsProof(w) {
		return false
	}
	branchRef := "refs/heads/" + w.Branch
	defaultRef := "refs/heads/" + w.DefaultBranch
	if defaultRef == branchRef {
		return false
	}
	if _, err := gitOut(ctx, w.Project, "show-ref", "--verify", "--quiet", defaultRef); err != nil {
		return false
	}
	head, err := gitOut(ctx, w.Project, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return false
	}
	unique, err := gitOut(ctx, w.Project, "rev-list", "--max-count=1", head, "--not", defaultRef)
	return err == nil && unique != ""
}

// cleanupPrepareProof records, outside the cleanup locks, whether a branch
// with no delivery change is already on a default ref through a remote merge,
// a cherry-pick or a squash. MergedHead is set even when no proof is found, so
// cleanupWorktree knows the check was made for that head.
func cleanupPrepareProof(ctx context.Context, w *cleanupWorktree) error {
	if !cleanupWantsProof(w) {
		return nil
	}
	branchRef := "refs/heads/" + w.Branch
	defaultRef := "refs/heads/" + w.DefaultBranch
	if defaultRef == branchRef {
		return nil
	}
	if _, err := gitOut(ctx, w.Project, "show-ref", "--verify", "--quiet", defaultRef); err != nil {
		return nil
	}
	head, err := gitOut(ctx, w.Project, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return nil
	}
	unique, err := gitOut(ctx, w.Project, "rev-list", "--max-count=1", head, "--not", defaultRef)
	if err != nil {
		return err
	}
	if unique == "" {
		// cleanupWorktree sees the local ancestry itself.
		return nil
	}
	refs := []string{defaultRef}
	if w.RemoteDefaultRef != "" {
		refs = append(refs, w.RemoteDefaultRef)
	}
	w.MergedHead = head
	w.MergedReason = cleanupMergedElsewhere(ctx, w.Project, head, refs)
	return nil
}

// cleanupFetchTimeout bounds the fetch that looks for a merge on the remote.
// Cleanup keeps the refs it has when the fetch fails.
const cleanupFetchTimeout = 10 * time.Second

// cleanupSquashCommits is how far back on the default branch cleanup looks for
// a squash merge. Each commit costs one merge-tree call.
const cleanupSquashCommits = 200

// cleanupFetchDefault refreshes origin/<default> before cleanup judges a branch
// that no delivery change describes. It returns the remote-tracking ref, or ""
// when the repository has no origin, the fetch fails, or the ref is absent.
func cleanupFetchDefault(ctx context.Context, dir, defaultBranch string) string {
	remoteRef := "refs/remotes/origin/" + defaultBranch
	if _, err := gitOut(ctx, dir, "remote", "get-url", "origin"); err != nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, cleanupFetchTimeout)
	defer cancel()
	refspec := "+refs/heads/" + defaultBranch + ":" + remoteRef
	if _, err := gitOut(fetchCtx, dir, "fetch", "--quiet", "--no-tags", "origin", refspec); err != nil {
		return ""
	}
	if _, err := gitOut(ctx, dir, "show-ref", "--verify", "--quiet", remoteRef); err != nil {
		return ""
	}
	return remoteRef
}

// cleanupMergedElsewhere proves that head is already on one of the default
// refs without being an ancestor of the local default branch. It returns the
// cleanup log reason, or "" when no proof is found.
func cleanupMergedElsewhere(ctx context.Context, dir, head string, refs []string) string {
	for _, ref := range refs {
		if _, err := gitOut(ctx, dir, "merge-base", "--is-ancestor", head, ref); err == nil {
			if strings.HasPrefix(ref, "refs/remotes/") {
				return "merged-remote"
			}
			return "merged"
		}
	}
	for _, ref := range refs {
		if cleanupCherryMerged(ctx, dir, head, ref) {
			return "merged-cherry"
		}
	}
	for _, ref := range refs {
		if cleanupSquashMerged(ctx, dir, head, ref) {
			return "merged-squash"
		}
	}
	return ""
}

// cleanupCherryMerged reports whether every commit of head that is not on ref
// is patch-equivalent to a commit on ref, which is what `git cherry` shows
// with a "-" line. A cherry-pick or a rebase leaves that shape.
func cleanupCherryMerged(ctx context.Context, dir, head, ref string) bool {
	out, err := gitOut(ctx, dir, "cherry", ref, head)
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "- ") {
			return false
		}
	}
	return true
}

// cleanupSquashMerged reports whether a commit on ref has exactly the tree
// that merging head into that commit's parent would produce. A squash merge
// has that shape, including a squash commit made by a pull request. Only the
// commits since the merge base, up to cleanupSquashCommits, are checked.
func cleanupSquashMerged(ctx context.Context, dir, head, ref string) bool {
	return cleanupSquashCommit(ctx, dir, head, ref) != ""
}

// cleanupSquashCommit returns the commit on ref that squash-merged head, or ""
// when there is none. See cleanupSquashMerged.
func cleanupSquashCommit(ctx context.Context, dir, head, ref string) string {
	base, err := gitOut(ctx, dir, "merge-base", head, ref)
	if err != nil || base == "" {
		return ""
	}
	commits, err := gitOut(ctx, dir, "rev-list", "--parents", fmt.Sprintf("--max-count=%d", cleanupSquashCommits), ref, "--not", base)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(commits, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			// A root commit or a merge commit is not a squash.
			continue
		}
		tree, err := gitOut(ctx, dir, "merge-tree", "--write-tree", parts[1], head)
		if err != nil {
			continue
		}
		actual, err := gitOut(ctx, dir, "rev-parse", parts[0]+"^{tree}")
		if err == nil && tree != "" && tree == actual {
			return parts[0]
		}
	}
	return ""
}

// cleanupContainedMaxRefs bounds how many branches that contain a closed
// change's head are looked up at the gateway.
const cleanupContainedMaxRefs = 10

// cleanupPrepareContained records, outside the cleanup locks, whether the
// commits of a closed change are all on the branch of another open or merged
// change. It asks git for the other branches that contain the head, then asks
// the gateway whether one of them belongs to a live change. Rows that are not
// closed, or that the default branch already holds, are left alone.
func (d *Daemon) cleanupPrepareContained(ctx context.Context, project string, w *cleanupWorktree) error {
	if w.ChangeState != protocol.ChangeStopped || w.DefaultBranch == "" || w.MergedReason != "" {
		return nil
	}
	branchRef := "refs/heads/" + w.Branch
	head, err := gitOut(ctx, w.Project, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return nil
	}
	refs, err := gitOut(ctx, w.Project, "for-each-ref", "--format=%(refname)", "--contains", head, "refs/heads/")
	if err != nil {
		return err
	}
	checked := 0
	for _, ref := range strings.Split(refs, "\n") {
		other, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok || other == w.Branch || other == w.DefaultBranch {
			continue
		}
		if checked++; checked > cleanupContainedMaxRefs {
			break
		}
		q := protocol.ChangeListRequest{Project: project, Branch: other, Limit: 10}
		var page protocol.ChangeListResult
		if err := d.deliveryCall(ctx, protocol.FrameChangeList, q, &page); err != nil {
			return err
		}
		if page.Branch != other {
			return fmt.Errorf("gateway does not support branch lookup")
		}
		for _, c := range page.Changes {
			if c.Change.Branch == other && c.Change.ID != "" && c.Change.State != protocol.ChangeStopped {
				w.ContainedIn, w.ContainedHead = c.Change.ID, head
				return nil
			}
		}
	}
	return nil
}

// cleanupChange finds every change that has used the branch. Multiple matches
// are kept until all references can be resolved safely.
func (d *Daemon) cleanupChange(ctx context.Context, project string, w *cleanupWorktree) error {
	q := protocol.ChangeListRequest{Project: project, Branch: w.Branch, State: protocol.ChangeListAny, Limit: 100}
	var page protocol.ChangeListResult
	if err := d.deliveryCall(ctx, protocol.FrameChangeList, q, &page); err != nil {
		return err
	}
	if page.Branch != w.Branch {
		return fmt.Errorf("gateway does not support branch lookup")
	}
	w.ChangeChecked = true
	if len(page.Changes) > 1 || page.Next != "" {
		w.ChangeState = protocol.ChangeOpen
		return nil
	}
	if len(page.Changes) == 1 {
		c := page.Changes[0].Change
		w.ChangeState, w.MergedCommit = c.State, c.MergedCommit
		if c.UpdatedAt != 0 {
			w.ChangeEndedAt = time.Unix(c.UpdatedAt, 0)
		}
	}
	return nil
}

// observeLegacyWorktrees records unused managed worktrees whose worker records
// were already discarded so the same cleanup checks can be applied.
func (d *Daemon) observeLegacyWorktrees(ctx context.Context, project string, now time.Time) error {
	data, err := gitOut(ctx, project, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	d.qmu.Lock()
	defer d.qmu.Unlock()
	d.wmu.Lock()
	defer d.wmu.Unlock()
	queued, err := d.st.queue()
	if err != nil {
		return err
	}
	rows, err := d.st.workers()
	if err != nil {
		return err
	}
	active := make(map[string]bool)
	harnesses := make(map[string]string)
	for id := range d.workers {
		active[id] = true
	}
	for _, row := range rows {
		harnesses[row.ID] = row.Harness
		if row.EndedReason == "" {
			active[row.ID] = true
		}
	}
	var path, branch string
	record := func() error {
		id := filepath.Base(path)
		actual, ok := strings.CutPrefix(branch, "refs/heads/")
		if !strings.HasPrefix(id, "w_") || path != filepath.Join(d.worktreesDir(), id) || !ok || !cleanupBranch(actual, id) || active[id] || d.startingBases[actual] > 0 {
			return nil
		}
		for _, q := range queued {
			if q.Base == actual || filepath.Clean(q.Path) == path {
				return nil
			}
		}
		_, err := d.st.db.Exec(`INSERT INTO cleanup_worktrees(worker_id,project,path,branch,ended_at,harness) VALUES(?,?,?,?,?,?) ON CONFLICT(worker_id) DO NOTHING`, id, project, path, actual, now.UnixNano(), harnesses[id])
		return err
	}
	for _, line := range strings.Split(data, "\x00") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			branch = strings.TrimPrefix(line, "branch ")
		case line == "":
			if path != "" {
				if err := record(); err != nil {
					return err
				}
			}
			path, branch = "", ""
		}
	}
	return nil
}
