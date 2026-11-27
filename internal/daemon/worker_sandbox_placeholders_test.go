package daemon

import (
	"os"
	"path/filepath"
	"strings"
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

// info/exclude is shared by the owner's checkout and every worktree of the repo.
// A worker worktree must not write placeholder lines there, or a .mcp.json or
// .vscode the owner adds later would vanish from git status.
func TestWorkerWorktreeAddsNoSandboxPlaceholderExcludes(t *testing.T) {
	repo := newRepo(t, "placeholder-exclude")
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_exclude_test", path: repo}
	if err := d.createWorktree(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(repo, filepath.FromSlash(gitOutT(t, repo, "rev-parse", "--git-path", "info/exclude")))
	raw, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	for _, name := range sandboxPlaceholderNames {
		if have["/"+name] {
			t.Fatalf("info/exclude hides %s after a worker worktree was made:\n%s", name, raw)
		}
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
	if d.saveWorkerChanges(w, "save", "test", false).SHA == "" {
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
	if d.saveWorkerChanges(w, "save", "test", false).SHA == "" {
		t.Fatal("nothing committed")
	}
	if got := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != ".bashrc\n.gitconfig" {
		t.Fatalf("leftover commit has %q, want .bashrc and .gitconfig", got)
	}
}

// A sandboxed worker that runs commands in a subfolder gets the same empty
// placeholders there. They are not work at any depth.
func TestLeftoverSkipsSandboxPlaceholdersInSubfolders(t *testing.T) {
	repo := newRepo(t, "placeholder-subdirs")
	for _, name := range []string{"sub/.claude/agents", "sub/.mcp.json", "sub/deeper/.bashrc", "internal/gateway/.claude/settings.json"} {
		file := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_subdirs_test", worktree: repo}
	if d.saveWorkerChanges(w, "save", "test", false).SHA == "" {
		t.Fatal("nothing committed")
	}
	if got := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "keep.txt" {
		t.Fatalf("leftover commit has %q, want only keep.txt", got)
	}
}

// A non-empty file with a placeholder name in a subfolder is work and is kept.
func TestLeftoverKeepsRealFilesWithPlaceholderNamesInSubfolders(t *testing.T) {
	repo := newRepo(t, "placeholder-subdirs-real")
	for name, data := range map[string][]byte{
		"sub/.mcp.json":             []byte("{}\n"),
		"sub/.claude/settings.json": []byte("{}\n"),
		"sub/.claude/agents":        nil,
	} {
		file := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_subdirs_real_test", worktree: repo}
	if d.saveWorkerChanges(w, "save", "test", false).SHA == "" {
		t.Fatal("nothing committed")
	}
	if got := gitOutT(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "sub/.claude/settings.json\nsub/.mcp.json" {
		t.Fatalf("leftover commit has %q, want the two non-empty files", got)
	}
}

// The hand-back's untracked list tells the owner what the worker left behind.
// Placeholders are not work, so they must not be listed as untracked files.
func TestHandbackFactsSkipSandboxPlaceholders(t *testing.T) {
	repo := newRepo(t, "placeholder-facts")
	base := gitOutT(t, repo, "rev-parse", "HEAD")
	writeSandboxPlaceholders(t, repo)
	d := commitTestDaemon(t)
	w := &worker{id: "w_placeholder_facts_test", worktree: repo, base: base}
	if stat, changed := d.worktreeFacts(w); changed {
		t.Fatalf("placeholders alone count as work:\n%s", stat)
	}
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, _ := d.worktreeFacts(w)
	if stat != "untracked: keep.txt" {
		t.Fatalf("untracked list is %q, want only keep.txt", stat)
	}
}

// In a Claude worker's own git commands, the empty files Claude Code's sandbox
// makes do not show as untracked, so git add -A and scripts that read git
// status skip them. A real folder with a placeholder name still shows, and the
// excludes the worker had before still apply.
func TestClaudeWorkerGitHidesSandboxPlaceholders(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "git", "ignore"), []byte("*.swp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := commitTestDaemon(t)
	w, wt := commitTestWorker(t, d)
	w.harness = "claude"
	writeSandboxPlaceholders(t, wt)
	writeSandboxPlaceholders(t, filepath.Join(wt, "sub"))
	if err := os.MkdirAll(filepath.Join(wt, "real", ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"notes.swp", "real/.vscode/settings.json", "work.go"} {
		writeFile(t, wt, f, []byte("x"))
	}
	status := gitWithEnv(t, wt, d.workerCommitEnv(w), "status", "--porcelain", "--untracked-files=all")
	got := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(status), "\n") {
		got[strings.TrimPrefix(l, "?? ")] = true
	}
	want := map[string]bool{"real/.vscode/settings.json": true, "work.go": true}
	if len(got) != len(want) || !got["work.go"] || !got["real/.vscode/settings.json"] {
		t.Fatalf("untracked files in the worker's git status:\n%s\nwant only %v", status, want)
	}
}
