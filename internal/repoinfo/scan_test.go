package repoinfo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// mkRepo makes a repository with one commit at base/rel.
func mkRepo(t *testing.T, base, rel string) string {
	t.Helper()
	dir := filepath.Join(base, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runIn(t, dir, "init", "-q", "-b", "main")
	runIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	return dir
}

func paths(es []protocol.ProjectEntry) string {
	var p []string
	for _, e := range es {
		p = append(p, e.Path)
	}
	return strings.Join(p, "\n")
}

func skipped(res ScanResult) map[string]string {
	m := map[string]string{}
	for _, s := range res.Skipped {
		m[s.Path] = s.Reason
	}
	return m
}

func TestInspectResolvesMainTreeRemoteAndDefaultBranch(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	main := mkRepo(t, base, "shop")
	runIn(t, main, "remote", "add", "origin", "https://bob:hunter2@example.com/team/shop.git")
	runIn(t, main, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	runIn(t, main, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	wt := filepath.Join(base, "shop-feature")
	runIn(t, main, "worktree", "add", "-q", "-b", "feature", wt)
	if err := os.MkdirAll(filepath.Join(main, "pkg", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := New()
	for _, in := range []string{main, wt, filepath.Join(main, "pkg", "deep")} {
		e, err := r.Inspect(in)
		if err != nil {
			t.Fatalf("Inspect(%s): %v", in, err)
		}
		if e.Path != main || e.Name != "shop" || e.Remote != "https://example.com/team/shop.git" || e.DefaultBranch != "trunk" {
			t.Errorf("Inspect(%s) = %+v", in, e)
		}
	}
}

func TestInspectRegistersTheRealFolderOfASymlink(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	real := mkRepo(t, base, "real-name")
	if err := os.Symlink(real, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	e, err := New().Inspect(filepath.Join(base, "alias"))
	if err != nil || e.Path != real || e.Name != "real-name" {
		t.Fatalf("%v %+v", err, e)
	}
}

func TestInspectSubmoduleIsItsOwnProject(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	lib := mkRepo(t, base, "lib")
	app := mkRepo(t, base, "app")
	runIn(t, app, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "vendored-lib")
	e, err := New().Inspect(filepath.Join(app, "vendored-lib"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Path != filepath.Join(app, "vendored-lib") || e.Name != "vendored-lib" {
		t.Fatalf("submodule: %+v", e)
	}
}

func TestInspectErrors(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	plain := filepath.Join(base, "plain")
	os.MkdirAll(plain, 0o755)
	file := filepath.Join(base, "file.txt")
	os.WriteFile(file, []byte("x"), 0o644)
	bare := filepath.Join(base, "bare.git")
	os.MkdirAll(bare, 0o755)
	runIn(t, bare, "init", "-q", "--bare")
	for in, want := range map[string]string{
		filepath.Join(base, "gone"): "does not exist",
		file:                        "not a folder",
		plain:                       "not a git working tree",
		bare:                        "not a git working tree",
	} {
		_, err := New().Inspect(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Inspect(%s) = %v, want %q", in, err, want)
		}
	}
}

// code is a tree like a developer's ~/code.
func codeTree(t *testing.T) (base string, repos []string, wt string) {
	t.Helper()
	base = evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	a := mkRepo(t, base, "alpha")
	b := mkRepo(t, base, "beta")
	c := mkRepo(t, base, "group/gamma")
	d := mkRepo(t, base, "group/deep/delta") // level 3
	mkRepo(t, base, "group/deep/x/y/too-deep")
	mkRepo(t, base, "node_modules/dep")
	mkRepo(t, base, ".hidden/secret")
	mkRepo(t, base, "vendor/lib")
	mkRepo(t, base, "alpha/inner") // inside a repository: never entered
	os.MkdirAll(filepath.Join(base, "plain", "docs"), 0o755)
	wt = filepath.Join(base, "alpha-feature")
	runIn(t, a, "worktree", "add", "-q", "-b", "feature", wt)
	if err := os.Symlink(a, filepath.Join(base, "link-to-alpha")); err != nil {
		t.Fatal(err)
	}
	return base, []string{a, b, d, c}, wt
}

func TestScanFindsReposAndSkipsTheRest(t *testing.T) {
	needGit(t)
	base, repos, wt := codeTree(t)
	res, err := New().Scan(context.Background(), base, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(res.Projects), strings.Join(repos, "\n"); got != want {
		t.Fatalf("projects:\n%s\nwant:\n%s", got, want)
	}
	sk := skipped(res)
	if r := sk[wt]; r != "linked worktree of "+repos[0] {
		t.Errorf("worktree skip reason: %q (all: %v)", r, sk)
	}
	if sk[filepath.Join(base, "node_modules")] == "" || sk[filepath.Join(base, "vendor")] == "" {
		t.Errorf("node_modules and vendor should be reported: %v", sk)
	}
	if len(sk) != 3 || res.Overflow != 0 {
		t.Errorf("unexpected skips %v overflow %d", sk, res.Overflow)
	}
}

func TestScanDepth(t *testing.T) {
	needGit(t)
	base, repos, _ := codeTree(t)
	res, err := New().Scan(context.Background(), base, ScanOptions{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := paths(res.Projects), repos[0]+"\n"+repos[1]; got != want {
		t.Fatalf("depth 1:\n%s\nwant:\n%s", got, want)
	}
	res, _ = New().Scan(context.Background(), base, ScanOptions{Depth: 5})
	if !strings.Contains(paths(res.Projects), "too-deep") {
		t.Fatalf("depth 5 should reach too-deep:\n%s", paths(res.Projects))
	}
	if _, err := New().Scan(context.Background(), base, ScanOptions{Depth: protocol.MaxScanDepth + 1}); err == nil {
		t.Fatal("a depth above the maximum must be refused")
	}
}

func TestScanOfARepositoryFindsIt(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	a := mkRepo(t, base, "alpha")
	mkRepo(t, base, "alpha/inner")
	res, err := New().Scan(context.Background(), a, ScanOptions{})
	if err != nil || paths(res.Projects) != a {
		t.Fatalf("%v %s", err, paths(res.Projects))
	}
}

func TestScanThroughASymlinkedStartFolder(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	a := mkRepo(t, base, "real/alpha")
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "code")); err != nil {
		t.Fatal(err)
	}
	res, err := New().Scan(context.Background(), filepath.Join(base, "code"), ScanOptions{})
	if err != nil || paths(res.Projects) != a || len(res.Skipped) != 0 {
		t.Fatalf("%v projects %s skipped %v", err, paths(res.Projects), res.Skipped)
	}
}

func TestScanSkipsWorktreeFoldersEvenWhenStartedInside(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	keep := mkRepo(t, base, ".claude/keep")
	mkRepo(t, base, ".claude/worktrees/agent-1")
	mkRepo(t, base, ".worktrees/w")
	res, err := New().Scan(context.Background(), filepath.Join(base, ".claude"), ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if paths(res.Projects) != keep {
		t.Fatalf("projects: %s", paths(res.Projects))
	}
	if skipped(res)[filepath.Join(base, ".claude", "worktrees")] != "worktree folder" {
		t.Fatalf("skips: %v", res.Skipped)
	}
	// From the parent, the hidden folders are not entered at all.
	res, _ = New().Scan(context.Background(), base, ScanOptions{})
	if len(res.Projects) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("hidden folders were entered: %+v", res)
	}
}

func TestScanCapReportsOverflow(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	for i := 0; i < 5; i++ {
		mkRepo(t, base, fmt.Sprintf("r%d", i))
	}
	res, err := New().Scan(context.Background(), base, ScanOptions{Max: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projects) != 3 || res.Overflow != 2 {
		t.Fatalf("projects %d overflow %d", len(res.Projects), res.Overflow)
	}
}

func TestScanErrors(t *testing.T) {
	base := evalDir(t)
	file := filepath.Join(base, "f")
	os.WriteFile(file, nil, 0o644)
	for _, in := range []string{filepath.Join(base, "gone"), file} {
		if _, err := New().Scan(context.Background(), in, ScanOptions{}); err == nil {
			t.Errorf("Scan(%s) should fail", in)
		}
	}
}

func TestScanBrokenGitFolderIsSkippedWithReason(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	os.MkdirAll(filepath.Join(base, "broken", ".git"), 0o755) // an empty .git folder
	good := mkRepo(t, base, "good")
	res, err := New().Scan(context.Background(), base, ScanOptions{})
	if err != nil || paths(res.Projects) != good {
		t.Fatalf("%v %s", err, paths(res.Projects))
	}
	if r := skipped(res)[filepath.Join(base, "broken")]; r != "not a git working tree" {
		t.Fatalf("skips: %v", res.Skipped)
	}
}
