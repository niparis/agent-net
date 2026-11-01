package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Owner session worktrees (docs/design/start-sessions.md, build plan part 9).
// They live next to the project, not under the daemon's state folder, and
// they belong to the owner: worker cleanup never removes them. Open question
// 4 recommends this place, and this part follows that recommendation.

// ownerWorktreeRoot is the folder that holds the project's owner worktrees:
// ~/code/agent-net.worktrees for ~/code/agent-net.
func ownerWorktreeRoot(projectPath string) string {
	clean := filepath.Clean(projectPath)
	return filepath.Join(filepath.Dir(clean), filepath.Base(clean)+".worktrees")
}

// createOwnerWorktree makes a new worktree next to the project, on a new
// branch, and returns its folder, its branch and the branch it started from.
// An empty branch asks for the lowest free agent-net/<project>-<n>.
func (d *Daemon) createOwnerWorktree(ctx context.Context, projectPath, project, branch string) (dir, branchName, base string, err error) {
	return d.createOwnerWorktreeChecked(ctx, projectPath, project, branch, false)
}

func (d *Daemon) createOwnerWorktreeChecked(ctx context.Context, projectPath, project, branch string, proposal bool) (dir, branchName, base string, err error) {
	if !isGitRepo(ctx, projectPath) {
		return "", "", "", fmt.Errorf("cannot make a worktree: %s is not a git repository with a commit", projectPath)
	}
	base, _ = gitOut(ctx, projectPath, "symbolic-ref", "--short", "HEAD")
	if base == "" {
		base = "HEAD"
	}
	if strings.TrimSpace(branch) == "" {
		branch, err = d.lowestFreeOwnerBranch(ctx, projectPath, project)
		if err != nil {
			return "", "", "", err
		}
	} else {
		branch = strings.TrimSpace(branch)
		if strings.HasPrefix(branch, "-") {
			return "", "", "", fmt.Errorf("invalid branch %q", branch)
		}
		if _, err := gitOut(ctx, projectPath, "check-ref-format", "--branch", branch); err != nil {
			return "", "", "", fmt.Errorf("invalid branch %q", branch)
		}
	}
	if _, err := gitOut(ctx, projectPath, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}"); err == nil {
		return "", "", "", fmt.Errorf("branch %s already exists", branch)
	}
	root := ownerWorktreeRoot(projectPath)
	dir = filepath.Join(root, branch)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", "", "", fmt.Errorf("invalid branch %q", branch)
	}
	if proposal {
		if err := checkOwnerHome(dir); err != nil {
			return "", "", "", err
		}
	}
	if _, err := os.Stat(dir); err == nil {
		return "", "", "", fmt.Errorf("the worktree folder already exists: %s", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", "", "", err
	}
	if _, err := gitOutWithContext(ctx, projectPath, "worktree", "add", dir, "-b", branch, "HEAD"); err != nil {
		var gitErr *workerGitError
		if errors.As(err, &gitErr) {
			message := gitErr.stderr
			if len(message) > 200 {
				message = message[len(message)-200:]
			}
			return "", "", "", fmt.Errorf("cannot make a worktree: git worktree add: %s: %w", message, gitErr.err)
		}
		return "", "", "", fmt.Errorf("cannot make a worktree: %w", err)
	}
	d.markOwnerWorktree(dir)
	return dir, branch, base, nil
}

// lowestFreeOwnerBranch is agent-net/<project>-<n> with the lowest n not used
// by an existing branch.
func (d *Daemon) lowestFreeOwnerBranch(ctx context.Context, projectPath, project string) (string, error) {
	prefix := protocol.OwnerWorktreeBranchPrefix(project)
	out, err := gitOut(ctx, projectPath, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+prefix+"*")
	if err != nil {
		return "", fmt.Errorf("cannot make a worktree: %w", err)
	}
	taken := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(line, prefix)); err == nil && n > 0 {
			taken[n] = true
		}
	}
	n := 1
	for taken[n] {
		n++
	}
	return protocol.OwnerWorktreeBranch(project, n), nil
}

// markOwnerWorktree records a folder that belongs to the owner, so the worker
// cleanup cannot remove it even if a record pointed there.
func (d *Daemon) markOwnerWorktree(dir string) {
	if dir == "" {
		return
	}
	d.mu.Lock()
	if d.ownerWorktrees == nil {
		d.ownerWorktrees = make(map[string]bool)
	}
	d.ownerWorktrees[filepath.Clean(dir)] = true
	d.mu.Unlock()
}

// isOwnerWorktree tells whether dir is an owner session's worktree.
func (d *Daemon) isOwnerWorktree(dir string) bool {
	if dir == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ownerWorktrees[filepath.Clean(dir)]
}
