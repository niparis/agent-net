package repoinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestStripCredentials(t *testing.T) {
	cases := map[string]string{
		"https://user:secret@github.com/o/r.git":  "https://github.com/o/r.git",
		"https://ghp_token123@github.com/o/r.git": "https://github.com/o/r.git",
		"https://github.com/o/r.git":              "https://github.com/o/r.git",
		"https://u:p@host:8443/o/r.git?x=1#frag":  "https://host:8443/o/r.git",
		"ssh://git:pw@host/o/r.git":               "ssh://git@host/o/r.git",
		"ssh://git@host/o/r.git":                  "ssh://git@host/o/r.git",
		"git@github.com:o/r.git":                  "git@github.com:o/r.git",
		"/srv/repos/r.git":                        "/srv/repos/r.git",
		"https://u:p%zz@host/o/r.git":             "https://host/o/r.git", // does not parse
		"  https://tok@example.com/a  ":           "https://example.com/a",
		"https://us er:pw@host/r":                 "https://host/r",
		"":                                        "",
	}
	for in, want := range cases {
		if got := StripCredentials(in); got != want {
			t.Errorf("StripCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

func runIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func evalDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLookupRealRepo(t *testing.T) {
	needGit(t)
	root := filepath.Join(evalDir(t), "my-project")
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	runIn(t, root, "init", "-q", "-b", "feature-x")
	runIn(t, root, "remote", "add", "origin", "https://bob:hunter2@example.com/team/my-project.git")
	got := New().Lookup(sub)
	if got.Root != root || got.Project != "my-project" || got.Branch != "feature-x" {
		t.Fatalf("got %+v", got)
	}
	if got.Remote != "https://example.com/team/my-project.git" {
		t.Fatalf("remote %q still has credentials or is wrong", got.Remote)
	}
}

func TestLookupNoRepoUsesFolderName(t *testing.T) {
	needGit(t)
	dir := filepath.Join(evalDir(t), "plain-folder")
	os.MkdirAll(dir, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	got := New().Lookup(dir)
	if got.Root != "" || got.Remote != "" || got.Branch != "" || got.Project != "plain-folder" {
		t.Fatalf("got %+v", got)
	}
}

func TestLookupMissingFolderAndEmptyCwd(t *testing.T) {
	r := New()
	if got := r.Lookup(""); got != (r.Lookup("  ")) || got.Project != "" {
		t.Fatalf("empty cwd: %+v", got)
	}
	got := r.Lookup(filepath.Join(evalDir(t), "gone"))
	if got.Project != "gone" || got.Root != "" {
		t.Fatalf("missing folder: %+v", got)
	}
}

func TestDetachedHead(t *testing.T) {
	needGit(t)
	root := evalDir(t)
	runIn(t, root, "init", "-q", "-b", "main")
	runIn(t, root, "commit", "-q", "--allow-empty", "-m", "one")
	runIn(t, root, "checkout", "-q", "--detach")
	got := New().Lookup(root)
	if len(got.Branch) < len("detached@")+4 || got.Branch[:9] != "detached@" {
		t.Fatalf("branch %q", got.Branch)
	}
}

func TestCachePerFolderAndBranchRefresh(t *testing.T) {
	calls := 0
	branch := "main"
	now := time.Unix(1000, 0)
	r := New()
	r.now = func() time.Time { return now }
	r.git = func(ctx context.Context, dir string, args ...string) (string, error) {
		calls++
		switch args[0] {
		case "rev-parse":
			return "/work/proj", nil
		case "config":
			return "git@host:o/proj.git", nil
		case "symbolic-ref":
			return branch, nil
		}
		return "", context.Canceled
	}
	got := r.Lookup("/work/proj/sub")
	if got.Project != "proj" || got.Branch != "main" || calls != 4 {
		t.Fatalf("first lookup: %+v after %d calls", got, calls)
	}
	r.Lookup("/work/proj/sub")
	if calls != 4 {
		t.Fatalf("second lookup ran git again (%d calls)", calls)
	}
	branch = "other"
	if got := r.Lookup("/work/proj/sub"); got.Branch != "main" {
		t.Fatalf("Lookup within the branch TTL changed the branch: %q", got.Branch)
	}
	if got := r.Refresh("/work/proj/sub"); got.Branch != "other" || calls != 5 {
		t.Fatalf("Refresh: %+v after %d calls", got, calls)
	}
	branch = "third"
	now = now.Add(r.BranchTTL + time.Second)
	if got := r.Lookup("/work/proj/sub"); got.Branch != "third" || calls != 6 {
		t.Fatalf("Lookup after the TTL: %+v after %d calls", got, calls)
	}
	// Another folder has its own entry.
	r.Lookup("/work/other")
	if calls != 10 {
		t.Fatalf("a second folder should run 4 more commands, calls = %d", calls)
	}
	// The static part expires too.
	now = now.Add(r.StaticTTL + time.Second)
	r.Lookup("/work/proj/sub")
	if calls != 14 {
		t.Fatalf("static refresh should run 4 commands, calls = %d", calls)
	}
}

func TestSlowGitDoesNotBlockAndDoesNotFail(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := New()
	r.Timeout = 300 * time.Millisecond
	start := time.Now()
	got := r.Lookup(filepath.Join(evalDir(t), "slowrepo"))
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("lookup took %v with a 300ms timeout", d)
	}
	if got.Project != "slowrepo" || got.Root != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestMissingGitBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got := New().Lookup(filepath.Join(evalDir(t), "nogit"))
	if got.Project != "nogit" {
		t.Fatalf("got %+v", got)
	}
}

// A linked git worktree belongs to the project of its main working tree.
func TestWorktreeUsesMainWorkingTree(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	main := filepath.Join(base, "tenebrae")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	runIn(t, main, "init", "-q", "-b", "main")
	runIn(t, main, "remote", "add", "origin", "https://example.com/o/tenebrae.git")
	runIn(t, main, "commit", "-q", "--allow-empty", "-m", "one")
	wt := filepath.Join(base, "agent-a76c0ffee")
	runIn(t, main, "worktree", "add", "-q", "-b", "task", wt)
	sub := filepath.Join(wt, "pkg")
	os.MkdirAll(sub, 0o755)

	r := New()
	got := r.Lookup(sub)
	want := protocol.RepoInfo{Root: main, Worktree: wt, Project: "tenebrae", Remote: "https://example.com/o/tenebrae.git", Branch: "task"}
	if got != want {
		t.Fatalf("worktree: got %+v, want %+v", got, want)
	}
	if got := r.Lookup(main); got.Root != main || got.Worktree != main || got.Project != "tenebrae" || got.Branch != "main" {
		t.Fatalf("main tree: %+v", got)
	}
}

// A submodule has its own repository: it is not part of the parent project.
func TestSubmoduleKeepsItsOwnProject(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	lib := filepath.Join(base, "lib")
	os.MkdirAll(lib, 0o755)
	runIn(t, lib, "init", "-q", "-b", "main")
	runIn(t, lib, "commit", "-q", "--allow-empty", "-m", "lib")
	super := filepath.Join(base, "super")
	os.MkdirAll(super, 0o755)
	runIn(t, super, "init", "-q", "-b", "main")
	runIn(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "vendor/lib")
	got := New().Lookup(filepath.Join(super, "vendor", "lib"))
	if got.Project != "lib" || got.Root != filepath.Join(super, "vendor", "lib") {
		t.Fatalf("submodule: %+v", got)
	}
}
