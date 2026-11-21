package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSandboxPlaceholders makes the empty files Claude's sandbox leaves at
// every masked path under dir.
func writeSandboxPlaceholders(t *testing.T, dir string) {
	t.Helper()
	for _, name := range sandboxPlaceholderNames {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A new worker worktree hides the placeholders from git status, so the worker
// and the leftover commit never see them.
func TestWorkerWorktreeHidesSandboxPlaceholders(t *testing.T) {
	repo := newRepo(t, "placeholder-worktree")
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_worktree_test", path: repo}
	if err := d.createWorktree(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	writeSandboxPlaceholders(t, w.worktree)
	if err := os.WriteFile(filepath.Join(w.worktree, "keep.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitOutT(t, w.worktree, "status", "--porcelain", "--untracked-files=all"); got != "?? keep.txt" {
		t.Fatalf("status shows placeholders:\n%s", got)
	}
}

// An old worktree has no exclude for the placeholders. The leftover commit must
// still skip the zero-byte ones.
func TestLeftoverSkipsUntrackedSandboxPlaceholders(t *testing.T) {
	repo := newRepo(t, "placeholder-leftover")
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	writeSandboxPlaceholders(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_leftover_test", worktree: repo}
	if !d.saveWorkerChanges(w, "save", "test", false) {
		t.Fatal("nothing committed")
	}
	if got := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "keep.txt" {
		t.Fatalf("leftover commit has %q, want only keep.txt (base %s)", got, base)
	}
}

// A real file with a placeholder name is work: a tracked file that changed, and
// an untracked file that has content, are both committed.
func TestLeftoverKeepsRealFilesWithPlaceholderNames(t *testing.T) {
	repo := newRepo(t, "placeholder-real")
	if err := os.WriteFile(filepath.Join(repo, ".bashrc"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".bashrc")
	gitIn(t, repo, "commit", "-q", "-m", "empty bashrc")
	if err := os.WriteFile(filepath.Join(repo, ".bashrc"), []byte("export X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitconfig"), []byte("[user]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_real_test", worktree: repo}
	if !d.saveWorkerChanges(w, "save", "test", false) {
		t.Fatal("nothing committed")
	}
	if got := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != ".bashrc\n.gitconfig" {
		t.Fatalf("leftover commit has %q, want .bashrc and .gitconfig", got)
	}
}
