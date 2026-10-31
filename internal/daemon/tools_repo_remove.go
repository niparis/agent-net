package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

var repoSkillRoots = []string{".agents/skills", ".claude/skills", ".opencode/skills", ".pi/skills"}

const gitCommandTimeout = 20 * time.Second

func repoRemovalBranch(name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return "", errors.New("invalid skill name")
	}
	return "agent-net/remove-" + name, nil
}

func gitRefExists(ctx context.Context, repo, branch string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "show-ref", "--quiet", "--verify", "refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// probeRepoSkillRemoval is read-only. It binds a plan to the registered
// checkout's default branch head and records the exact tracked files and any
// local edits to the skill folders.
func (d *Daemon) probeRepoSkillRemoval(p protocol.ToolPlanProbe) protocol.ToolPlanProbeResult {
	out := protocol.ToolPlanProbeResult{Files: []string{}}
	conflict := func(reason string) protocol.ToolPlanProbeResult {
		out.Conflict = reason
		return out
	}
	branch, err := repoRemovalBranch(p.Name)
	if err != nil {
		return conflict(err.Error())
	}
	if p.Operation != "repo_remove" || p.Project == "" || p.Checkout == "" || !filepath.IsAbs(p.Checkout) || p.DefaultBranch == "" || p.Branch != branch {
		return conflict("project details unavailable")
	}
	if _, err = gitOut(context.Background(), p.Checkout, "rev-parse", "--show-toplevel"); err != nil {
		return conflict("project checkout unavailable")
	}
	if _, err = gitOut(context.Background(), p.Checkout, "check-ref-format", "--branch", p.DefaultBranch); err != nil {
		return conflict("invalid default branch")
	}
	base, err := gitOut(context.Background(), p.Checkout, "rev-parse", "--verify", "refs/heads/"+p.DefaultBranch+"^{commit}")
	if err != nil {
		return conflict("default branch unavailable")
	}
	out.BaseCommit = base
	exists, err := gitRefExists(context.Background(), p.Checkout, branch)
	if err != nil {
		return conflict("cannot inspect branch")
	}
	if exists {
		return conflict("branch exists")
	}
	paths := make([]string, 0, len(repoSkillRoots))
	for _, root := range repoSkillRoots {
		paths = append(paths, filepath.ToSlash(filepath.Join(root, p.Name)))
	}
	args := []string{"ls-tree", "-r", "-z", "--name-only", base, "--"}
	args = append(args, paths...)
	listed, err := gitOut(context.Background(), p.Checkout, args...)
	if err != nil {
		return conflict("cannot inspect default branch")
	}
	for _, file := range strings.Split(listed, "\x00") {
		if file != "" {
			out.Files = append(out.Files, file)
		}
	}
	sort.Strings(out.Files)
	if len(out.Files) == 0 {
		return conflict("skill is not committed on the default branch")
	}
	statusArgs := []string{"status", "--porcelain", "--untracked-files=all", "--"}
	statusArgs = append(statusArgs, paths...)
	status, err := gitOut(context.Background(), p.Checkout, statusArgs...)
	if err != nil {
		return conflict("cannot inspect local changes")
	}
	out.Dirty = status != ""
	return out
}

func (d *Daemon) removeRepoSkill(t protocol.ToolTarget) protocol.ToolResult {
	r := protocol.ToolResult{Item: t.Revision.Item, Revision: t.Revision.Revision, Index: t.Index, Branch: t.Target.Branch}
	fail := func(state, reason string) protocol.ToolResult { r.State, r.Reason = state, reason; return r }
	if t.Target.Action != "repo_remove" {
		return fail("conflict", "unsupported repo operation")
	}
	if t.Target.Conflict != "" {
		return fail("conflict", t.Target.Conflict)
	}
	probe := d.probeRepoSkillRemoval(protocol.ToolPlanProbe{Name: t.Name, Operation: "repo_remove", Project: t.Target.Project, Checkout: t.Target.Checkout, DefaultBranch: t.Target.DefaultBranch, Branch: t.Target.Branch})
	if probe.Conflict != "" {
		return fail("conflict", probe.Conflict)
	}
	if probe.BaseCommit != t.Target.BaseCommit || !equalStrings(probe.Files, t.Target.Files) {
		return fail("conflict", "repo removal plan changed")
	}
	branch, err := repoRemovalBranch(t.Name)
	if err != nil || branch != t.Target.Branch {
		return fail("conflict", "invalid branch")
	}
	state, err := filepath.Abs(d.cfg.StateDir)
	if err != nil || d.cfg.StateDir == "" {
		return fail("failed", "local state directory unavailable")
	}
	parent := filepath.Join(state, "repo-removals")
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return fail("failed", "cannot prepare temporary worktree directory")
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("failed", "unsafe temporary worktree directory")
	}
	worktree := filepath.Join(parent, protocol.NewID("remove"))
	if _, err = gitOut(context.Background(), t.Target.Checkout, "worktree", "add", worktree, "-b", branch, t.Target.BaseCommit); err != nil {
		exists, _ := gitRefExists(context.Background(), t.Target.Checkout, branch)
		if exists {
			return fail("conflict", "branch exists")
		}
		return fail("failed", "cannot create temporary worktree")
	}
	committed := false
	cleanup := func() error {
		_, removeErr := gitOut(context.Background(), t.Target.Checkout, "worktree", "remove", "--force", worktree)
		if removeErr != nil {
			return removeErr
		}
		if !committed {
			_, _ = gitOut(context.Background(), t.Target.Checkout, "branch", "-D", branch)
		}
		return nil
	}
	var opErr error
	folders := repoSkillFolders(t.Name, t.Target.Files)
	if len(folders) == 0 {
		opErr = errors.New("skill is not committed on the default branch")
	} else {
		args := append([]string{"rm", "-r", "--"}, folders...)
		if _, opErr = gitOut(context.Background(), worktree, args...); opErr == nil {
			subject := "Remove skill " + t.Name + " (requested in agent-net)"
			if _, opErr = gitOut(context.Background(), worktree, "commit", "-m", subject); opErr == nil {
				if hash, e := gitOut(context.Background(), worktree, "rev-parse", "--verify", "HEAD^{commit}"); e == nil {
					r.Commit = hash
					committed = true
				} else {
					opErr = e
				}
			}
		}
	}
	if err = cleanup(); err != nil {
		return fail("failed", "cannot remove temporary worktree")
	}
	if opErr != nil {
		return fail("failed", "cannot commit skill removal")
	}
	r.State = "applied"
	return r
}

func repoSkillFolders(name string, files []string) []string {
	var folders []string
	for _, root := range repoSkillRoots {
		dir := filepath.ToSlash(filepath.Join(root, name))
		for _, file := range files {
			if strings.HasPrefix(file, dir+"/") {
				folders = append(folders, dir)
				break
			}
		}
	}
	return folders
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
