package daemon

import (
	"context"
	"encoding/json"
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
		now := time.Now()
		if err := d.observeLegacyWorktrees(ctx, p.Path, now); err != nil {
			if d.log != nil {
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
			if err := d.cleanupWorktree(ctx, ready[i], now); err != nil && d.log != nil {
				d.Logf("worker.worktree_cleanup path=%q error=%v", ready[i].Path, err)
			}
		}
	}
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

// cleanupWorktree never removes a worktree with uncommitted files or a branch
// containing commits absent from the project default branch.
func (d *Daemon) cleanupWorktree(ctx context.Context, w cleanupWorktree, now time.Time) error {
	if w.EndedAt.IsZero() {
		return nil
	}
	if !strings.HasPrefix(w.ID, "w_") || filepath.Base(w.ID) != w.ID || !cleanupBranch(w.Branch, w.ID) || filepath.Clean(w.Path) != filepath.Join(d.worktreesDir(), w.ID) {
		return nil
	}
	d.resumeMu.Lock()
	defer d.resumeMu.Unlock()
	d.qmu.Lock()
	defer d.qmu.Unlock()
	if d.startingBases[w.Branch] > 0 {
		return nil
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if _, ok := d.workers[w.ID]; ok {
		return nil
	}
	if d.st != nil {
		var active int
		if err := d.st.db.QueryRow(`SELECT count(*) FROM workers WHERE id=? AND ended_reason=''`, w.ID).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return nil
		}
		queued, err := d.st.queue()
		if err != nil {
			return err
		}
		for _, q := range queued {
			if q.Base == w.Branch || filepath.Clean(q.Path) == filepath.Clean(w.Path) {
				return nil
			}
		}
		var current int
		if err := d.st.db.QueryRow(`SELECT count(*) FROM cleanup_worktrees WHERE worker_id=? AND project=? AND path=? AND branch=? AND ended_at=?`, w.ID, w.Project, w.Path, w.Branch, w.EndedAt.UnixNano()).Scan(&current); err != nil {
			return err
		}
		if current == 0 {
			return nil
		}
	}
	if d.isOwnerWorktree(w.Path) {
		return nil
	}
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
		case protocol.ChangeStopped:
			if w.ChangeEndedAt.IsZero() || now.Sub(w.ChangeEndedAt) <= 3*24*time.Hour {
				return nil
			}
			reason = "closed"
		default:
			return nil
		}
		if deadline := w.ChangeEndedAt.Add(30 * 24 * time.Hour); deadline.After(retainUntil) {
			retainUntil = deadline
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
	if !missing {
		if _, err := gitOut(ctx, w.Project, "worktree", "remove", w.Path); err != nil {
			return err
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
	if !retainUntil.IsZero() && now.Before(retainUntil) {
		return nil
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

// cleanupNeedsRemoteProof reports whether a row has no delivery change and its
// branch head is not on the local default branch, so cleanup must look at the
// remote and at cherry or squash proofs. It runs outside the cleanup locks.
func cleanupNeedsRemoteProof(ctx context.Context, w *cleanupWorktree) bool {
	if w.ChangeState != "" || !w.RetainUntil.IsZero() || w.DefaultBranch == "" {
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
	if w.ChangeState != "" || !w.RetainUntil.IsZero() || w.DefaultBranch == "" {
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
	base, err := gitOut(ctx, dir, "merge-base", head, ref)
	if err != nil || base == "" {
		return false
	}
	commits, err := gitOut(ctx, dir, "rev-list", "--parents", fmt.Sprintf("--max-count=%d", cleanupSquashCommits), ref, "--not", base)
	if err != nil {
		return false
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
			return true
		}
	}
	return false
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
