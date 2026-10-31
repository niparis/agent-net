package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestRepoSkillRemovalUsesTemporaryWorktreeAndKeepsBranch(t *testing.T) {
	repo := initRepoSkill(t)
	state := t.TempDir()
	d := &Daemon{cfg: Config{StateDir: state}}
	target := repoRemovalTarget(repo, "main")
	probe := d.probeRepoSkillRemoval(protocol.ToolPlanProbe{Name: target.Name, Operation: "repo_remove", Project: "repo", Checkout: repo, DefaultBranch: "main", Branch: target.Target.Branch})
	if probe.Conflict != "" {
		t.Fatalf("probe: %+v", probe)
	}
	target.Target.BaseCommit, target.Target.Files = probe.BaseCommit, probe.Files

	got := d.removeRepoSkill(target)
	if got.State != "applied" || got.Branch != "agent-net/remove-x" || len(got.Commit) != 40 {
		t.Fatalf("remove result: %+v", got)
	}
	if got.Reason != "" {
		t.Fatalf("remove reason: %s", got.Reason)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude/skills/x/SKILL.md")); err != nil {
		t.Fatalf("owner checkout changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents/skills/x/SKILL.md")); err != nil {
		t.Fatalf("owner checkout changed: %v", err)
	}
	if branch, _ := gitOut(context.Background(), repo, "branch", "--show-current"); branch != "main" {
		t.Fatalf("owner checkout branch changed to %q", branch)
	}
	if files, err := gitOut(context.Background(), repo, "ls-tree", "-r", "--name-only", got.Branch); err != nil || strings.Contains(files, "skills/x/") {
		t.Fatalf("branch still contains the skill: %q, %v", files, err)
	}
	if count, err := gitOut(context.Background(), repo, "rev-list", "--count", "main.."+got.Branch); err != nil || count != "1" {
		t.Fatalf("branch commit count %q, %v", count, err)
	}
	if author, err := gitOut(context.Background(), repo, "show", "-s", "--format=%an <%ae>", got.Commit); err != nil || author != "Repo Owner <owner@example.test>" {
		t.Fatalf("commit author %q, %v", author, err)
	}
	if subject, err := gitOut(context.Background(), repo, "show", "-s", "--format=%s", got.Commit); err != nil || subject != "Remove skill x (requested in agent-net)" {
		t.Fatalf("commit subject %q, %v", subject, err)
	}
	worktrees, err := gitOut(context.Background(), repo, "worktree", "list", "--porcelain")
	if err != nil || strings.Contains(worktrees, filepath.Join(state, "repo-removals")) {
		t.Fatalf("temporary worktree remains: %s, %v", worktrees, err)
	}
}

func TestRepoSkillRemovalRefusesExistingBranch(t *testing.T) {
	repo := initRepoSkill(t)
	if _, err := gitOut(context.Background(), repo, "branch", "agent-net/remove-x"); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	target := repoRemovalTarget(repo, "main")
	probe := d.probeRepoSkillRemoval(protocol.ToolPlanProbe{Name: target.Name, Operation: "repo_remove", Project: "repo", Checkout: repo, DefaultBranch: "main", Branch: target.Target.Branch})
	target.Target.BaseCommit, target.Target.Files = probe.BaseCommit, probe.Files
	got := d.removeRepoSkill(target)
	if got.State != "conflict" || got.Reason != "branch exists" {
		t.Fatalf("existing branch result: %+v", got)
	}
}

func TestRepoSkillRemovalPlanListsTrackedFilesAndLocalEdits(t *testing.T) {
	repo := initRepoSkill(t)
	if err := os.WriteFile(filepath.Join(repo, ".claude/skills/x/SKILL.md"), []byte("local edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{}
	got := d.probeRepoSkillRemoval(protocol.ToolPlanProbe{
		Name: "x", Operation: "repo_remove", Project: "repo", Checkout: repo, DefaultBranch: "main", Branch: "agent-net/remove-x",
	})
	if got.Conflict != "" || !got.Dirty {
		t.Fatalf("plan probe: %+v", got)
	}
	if len(got.Files) != 2 || got.Files[0] != ".agents/skills/x/SKILL.md" || got.Files[1] != ".claude/skills/x/SKILL.md" {
		t.Fatalf("tracked files: %v", got.Files)
	}
}

func initRepoSkill(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main", repo}, {"-C", repo, "config", "user.name", "Repo Owner"}, {"-C", repo, "config", "user.email", "owner@example.test"}} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	for _, dir := range []string{".claude/skills/x", ".agents/skills/x"} {
		path := filepath.Join(repo, dir, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("skill\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitOut(context.Background(), repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOut(context.Background(), repo, "commit", "-m", "Add skill"); err != nil {
		t.Fatal(err)
	}
	return repo
}

func repoRemovalTarget(repo, defaultBranch string) protocol.ToolTarget {
	return protocol.ToolTarget{
		Name:     "x",
		Revision: protocol.ToolRevision{Item: `skill-x`, Revision: 1, Version: "fixture", Assignment: protocol.ToolAssignment{Scope: "projects", Projects: []string{"repo"}}},
		Target:   protocol.ToolPlanTarget{NodeID: "node", Project: "repo", Checkout: repo, DefaultBranch: defaultBranch, Branch: "agent-net/remove-x", Files: []string{".agents/skills/x/SKILL.md", ".claude/skills/x/SKILL.md"}, Action: "repo_remove", State: "pending"},
	}
}
