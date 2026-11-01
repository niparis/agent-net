package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) cleanupProjectFolders(project string, now time.Time) {
	rules := projectWorkerRules(project)
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "cleanup:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "cleanup:")))
		if len(fields) != 2 {
			continue
		}
		age, err := time.ParseDuration(fields[1])
		if err != nil || age <= 0 {
			continue
		}
		path := fields[0]
		if !filepath.IsAbs(path) {
			path = filepath.Join(project, path)
		}
		path = filepath.Clean(path)
		// Never permit a declaration to target the checkout itself, its Git data,
		// or a parent containing the checkout.
		rel, err := filepath.Rel(path, project)
		if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			continue
		}
		worktrees := d.worktreesDir()
		if worktrees != "" && (path == worktrees || strings.HasPrefix(path, worktrees+string(filepath.Separator)) || strings.HasPrefix(worktrees, path+string(filepath.Separator))) {
			continue
		}
		gitDir := filepath.Join(project, ".git")
		relGit, err := filepath.Rel(gitDir, path)
		if err != nil || (relGit != ".." && !strings.HasPrefix(relGit, ".."+string(filepath.Separator))) {
			continue
		}
		inUse := d.cfg.CacheEntryInUse
		if inUse == nil {
			inUse = processHasOpenPath
		}
		n, err := trimFolder(path, now.Add(-age), inUse)
		if d.log != nil {
			d.Logf("worker.cleanup path=%q freed=%d error=%v", path, n, err)
		}
	}
}

func (d *Daemon) cleanupLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.cleanupProjects(ctx)
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
		if p.NodeID != d.cfg.NodeID || (p.Source != protocol.SourceRegistered && p.Source != protocol.SourceBoth) {
			continue
		}
		now := time.Now()
		d.cleanupProjectFolders(p.Path, now)
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
		for _, row := range rows {
			row.DefaultBranch = p.DefaultBranch
			if err := d.cleanupWorktree(ctx, row, now); err != nil && d.log != nil {
				d.Logf("worker.worktree_cleanup path=%q error=%v", row.Path, err)
			}
		}
	}
}

type cleanupWorktree struct {
	ID, Project, Path, Branch, Harness, DefaultBranch string
	EndedAt                                           time.Time
}

// cleanupWorktree never removes a worktree with uncommitted files or a branch
// containing commits absent from the project default branch.
func (d *Daemon) cleanupWorktree(ctx context.Context, w cleanupWorktree, _ time.Time) error {
	if w.EndedAt.IsZero() {
		return nil
	}
	if !strings.HasPrefix(w.ID, "w_") || filepath.Base(w.ID) != w.ID || w.Branch != protocol.WorktreeBranch(w.ID) || filepath.Clean(w.Path) != filepath.Join(d.worktreesDir(), w.ID) {
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
	defaultBranch := w.DefaultBranch
	if defaultBranch == "" {
		if ref, err := gitOut(ctx, w.Project, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil && strings.HasPrefix(ref, "refs/remotes/origin/") {
			defaultBranch = strings.TrimPrefix(ref, "refs/remotes/origin/")
		} else {
			for _, name := range []string{"main", "master"} {
				if _, err := gitOut(ctx, w.Project, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
					defaultBranch = name
					break
				}
			}
		}
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
	if unique != "" {
		return nil
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
	if _, err := gitOut(ctx, w.Project, "update-ref", "-d", branchRef, head); err != nil {
		return err
	}
	if d.st != nil {
		if _, err := d.st.db.Exec(`DELETE FROM cleanup_worktrees WHERE worker_id=? AND ended_at=?`, w.ID, w.EndedAt.UnixNano()); err != nil {
			return err
		}
	}
	if d.log != nil {
		d.Logf("worker.worktree_cleanup path=%q branch=%q freed=%d", w.Path, w.Branch, size)
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
		if !strings.HasPrefix(id, "w_") || path != filepath.Join(d.worktreesDir(), id) || branch != "refs/heads/"+protocol.WorktreeBranch(id) || active[id] || d.startingBases[protocol.WorktreeBranch(id)] > 0 {
			return nil
		}
		for _, q := range queued {
			if q.Base == protocol.WorktreeBranch(id) || filepath.Clean(q.Path) == path {
				return nil
			}
		}
		_, err := d.st.db.Exec(`INSERT INTO cleanup_worktrees(worker_id,project,path,branch,ended_at,harness) VALUES(?,?,?,?,?,?) ON CONFLICT(worker_id) DO NOTHING`, id, project, path, protocol.WorktreeBranch(id), now.UnixNano(), harnesses[id])
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
