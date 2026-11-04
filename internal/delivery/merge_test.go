package delivery

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mergeFx is a bare "origin", a checkout of it (RepoDir) with main checked
// out, and a path for the merge worktree.
type mergeFx struct {
	t      *testing.T
	root   string
	origin string
	repo   string
	wt     string
	base   string // first commit on main
}

func mergeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newMergeFx(t *testing.T) *mergeFx {
	t.Helper()
	root := t.TempDir()
	f := &mergeFx{t: t, root: root,
		origin: filepath.Join(root, "origin.git"),
		repo:   filepath.Join(root, "repo"),
		wt:     filepath.Join(root, "merge-wt"),
	}
	mergeGit(t, root, "init", "--bare", "-b", "main", f.origin)
	mergeGit(t, root, "clone", "-q", f.origin, f.repo)
	mergeGit(t, f.repo, "checkout", "-q", "-B", "main")
	f.write(f.repo, "README", "base\n")
	mergeGit(t, f.repo, "add", ".")
	mergeGit(t, f.repo, "commit", "-q", "-m", "base")
	mergeGit(t, f.repo, "push", "-q", "-u", "origin", "main")
	f.base = mergeGit(t, f.repo, "rev-parse", "HEAD")
	return f
}

func (f *mergeFx) write(dir, name, content string) {
	f.t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// branch creates a local branch from start with one commit and returns its
// head. main stays checked out in RepoDir.
func (f *mergeFx) branch(name, start, file, content string) string {
	f.t.Helper()
	mergeGit(f.t, f.repo, "checkout", "-q", "-B", name, start)
	f.write(f.repo, file, content)
	mergeGit(f.t, f.repo, "add", ".")
	mergeGit(f.t, f.repo, "commit", "-q", "-m", "change "+name)
	sha := mergeGit(f.t, f.repo, "rev-parse", "HEAD")
	mergeGit(f.t, f.repo, "checkout", "-q", "main")
	return sha
}

// advanceOrigin commits to main from a second clone and pushes it.
func (f *mergeFx) advanceOrigin(file, content string) string {
	f.t.Helper()
	other := filepath.Join(f.root, "other-"+file)
	mergeGit(f.t, f.root, "clone", "-q", f.origin, other)
	f.write(other, file, content)
	mergeGit(f.t, other, "add", ".")
	mergeGit(f.t, other, "commit", "-q", "-m", "other "+file)
	mergeGit(f.t, other, "push", "-q", "origin", "main")
	return mergeGit(f.t, other, "rev-parse", "HEAD")
}

func (f *mergeFx) originMain() string {
	return mergeGit(f.t, f.origin, "rev-parse", "refs/heads/main")
}

func (f *mergeFx) req(branch, head, mode string) MergeRequest {
	return MergeRequest{
		RepoDir: f.repo, WorktreeDir: f.wt, Remote: "origin",
		TargetBranch: "main", ChangeBranch: branch, HeadSHA: head, Mode: mode,
		Message:  "Merge " + branch,
		Identity: MergeIdentity{Name: "Merger", Email: "merger@example.com"},
		Push:     true, Timeout: 30 * time.Second,
	}
}

func (f *mergeFx) assertClean() {
	f.t.Helper()
	if s := mergeGit(f.t, f.wt, "status", "--porcelain", "--untracked-files=all"); s != "" {
		f.t.Fatalf("merge worktree not clean:\n%s", s)
	}
	cmd := exec.Command("git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
	cmd.Dir = f.wt
	if err := cmd.Run(); err == nil {
		f.t.Fatal("merge still in progress in the merge worktree")
	}
}

func TestMergeFFOnlyPushes(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")

	res, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if err != nil {
		t.Fatal(err)
	}
	want := MergeResult{MergedSHA: head, TargetBefore: f.base, TargetAfter: head, FastForward: true, Pushed: true}
	if res != want {
		t.Fatalf("result = %+v, want %+v", res, want)
	}
	if got := f.originMain(); got != head {
		t.Fatalf("origin main = %s, want %s", got, head)
	}
	// RepoDir's own main is not touched.
	if got := mergeGit(t, f.repo, "rev-parse", "main"); got != f.base {
		t.Fatalf("local main moved to %s", got)
	}
	f.assertClean()
	if got := mergeGit(t, f.wt, "rev-parse", "HEAD"); got != head {
		t.Fatalf("worktree HEAD = %s", got)
	}
}

func TestMergeNoPush(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	req := f.req("feat", head, MergeModeFFOnly)
	req.Push = false
	res, err := Merge(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed || res.TargetAfter != head {
		t.Fatalf("result = %+v", res)
	}
	if got := f.originMain(); got != f.base {
		t.Fatalf("origin moved to %s without Push", got)
	}
}

func TestMergeHeadMoved(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	newer := f.branch("feat", "feat", "b.txt", "b\n")

	_, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if !errors.Is(err, ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}
	if !strings.Contains(err.Error(), head) || !strings.Contains(err.Error(), newer) {
		t.Fatalf("error does not name both SHAs: %v", err)
	}
	if got := f.originMain(); got != f.base {
		t.Fatalf("origin changed to %s", got)
	}
	if _, serr := os.Stat(f.wt); serr == nil {
		t.Fatal("worktree created although the head moved")
	}
}

func TestMergeHeadOnRemoteOnly(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	mergeGit(t, f.repo, "push", "-q", "origin", "feat")
	mergeGit(t, f.repo, "branch", "-q", "-D", "feat")

	if _, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly)); err != nil {
		t.Fatal(err)
	}
	if got := f.originMain(); got != head {
		t.Fatalf("origin main = %s", got)
	}
}

func TestMergeChangeBranchMissing(t *testing.T) {
	f := newMergeFx(t)
	_, err := Merge(context.Background(), f.req("nope", f.base, MergeModeFFOnly))
	if !errors.Is(err, ErrChangeBranchMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeNotFastForward(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	moved := f.advanceOrigin("o.txt", "o\n")

	_, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, want ErrNotFastForward", err)
	}
	if got := f.originMain(); got != moved {
		t.Fatalf("origin main = %s, want %s", got, moved)
	}
	f.assertClean()
}

func TestMergeCommitPushes(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	moved := f.advanceOrigin("o.txt", "o\n")

	req := f.req("feat", head, MergeModeCommit)
	req.Message = "Merge feat: add a"
	res, err := Merge(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.FastForward || !res.Pushed || res.TargetBefore != moved || res.MergedSHA == head || res.MergedSHA != res.TargetAfter {
		t.Fatalf("result = %+v", res)
	}
	if got := f.originMain(); got != res.MergedSHA {
		t.Fatalf("origin main = %s, want %s", got, res.MergedSHA)
	}
	parents := strings.Fields(mergeGit(t, f.origin, "rev-list", "--parents", "-n", "1", res.MergedSHA))
	if len(parents) != 3 || parents[1] != moved || parents[2] != head {
		t.Fatalf("parents = %v", parents)
	}
	if got := mergeGit(t, f.origin, "log", "-1", "--format=%s|%an|%ae|%cn|%ce", res.MergedSHA); got != "Merge feat: add a|Merger|merger@example.com|Merger|merger@example.com" {
		t.Fatalf("commit = %q", got)
	}
	// both files are in the result
	for _, n := range []string{"a.txt", "o.txt"} {
		if _, err := os.Stat(filepath.Join(f.wt, n)); err != nil {
			t.Fatal(err)
		}
	}
	f.assertClean()
}

func TestMergeCommitDefaultMessage(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	req := f.req("feat", head, MergeModeCommit)
	req.Message = ""
	res, err := Merge(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := mergeGit(t, f.origin, "log", "-1", "--format=%s", res.MergedSHA); got != "Merge feat into main" {
		t.Fatalf("subject = %q", got)
	}
}

func TestMergeIdentityIgnoresInheritedAuthor(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "intruder")
	}
	res, err := Merge(context.Background(), f.req("feat", head, MergeModeCommit))
	if err != nil {
		t.Fatal(err)
	}
	got := mergeGit(t, f.origin, "log", "-1", "--format=%an|%ae|%cn|%ce", res.MergedSHA)
	if got != "Merger|merger@example.com|Merger|merger@example.com" {
		t.Fatalf("merge commit identity = %q", got)
	}
}

func TestMergeConflictLeavesCleanWorktree(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "README", "feature side\n")
	moved := f.advanceOrigin("README", "main side\n")

	_, err := Merge(context.Background(), f.req("feat", head, MergeModeCommit))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	f.assertClean()
	if got := f.originMain(); got != moved {
		t.Fatalf("origin main = %s, want %s", got, moved)
	}

	// The same worktree is usable for the next change.
	ok := f.branch("feat2", "origin/main", "z.txt", "z\n")
	res, err := Merge(context.Background(), f.req("feat2", ok, MergeModeCommit))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.originMain(); got != res.MergedSHA {
		t.Fatalf("origin main = %s", got)
	}
}

func TestMergeDirtyWorktreeRefused(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "one.txt", "1\n")
	req := f.req("one", h1, MergeModeFFOnly)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	h2 := f.branch("two", f.base, "two.txt", "2\n")

	cases := map[string]func(){
		"untracked": func() { f.write(f.wt, "scratch.txt", "x\n") },
		"modified":  func() { f.write(f.wt, "README", "edited\n") },
	}
	for name, dirty := range cases {
		t.Run(name, func(t *testing.T) {
			dirty()
			before := mergeGit(t, f.wt, "rev-parse", "HEAD")
			_, err := Merge(context.Background(), f.req("two", h2, MergeModeFFOnly))
			if !errors.Is(err, ErrDirtyWorktree) {
				t.Fatalf("err = %v, want ErrDirtyWorktree", err)
			}
			if got := mergeGit(t, f.wt, "rev-parse", "HEAD"); got != before {
				t.Fatal("dirty worktree was moved")
			}
			if got := f.originMain(); got != f.base {
				t.Fatalf("origin changed to %s", got)
			}
			mergeGit(t, f.wt, "checkout", "-q", "--", ".")
			mergeGit(t, f.wt, "clean", "-fdq")
		})
	}
}

func TestMergeReusesCleanWorktree(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "one.txt", "1\n")
	if _, err := Merge(context.Background(), f.req("one", h1, MergeModeFFOnly)); err != nil {
		t.Fatal(err)
	}
	h2 := f.branch("two", h1, "two.txt", "2\n")
	res, err := Merge(context.Background(), f.req("two", h2, MergeModeFFOnly))
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetBefore != h1 || res.TargetAfter != h2 {
		t.Fatalf("result = %+v", res)
	}
	f.assertClean()
}

func TestMergeRemoteMovedBeforePush(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")

	var moved string
	m := &mergeRunner{beforePush: func() { moved = f.advanceOrigin("o.txt", "o\n") }}
	res, err := m.merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("err = %v, want ErrRemoteMoved", err)
	}
	if res.Pushed {
		t.Fatal("Pushed is true")
	}
	if got := f.originMain(); got != moved {
		t.Fatalf("origin main = %s, want the other push %s (no force)", got, moved)
	}
}

func TestMergeSecondCallIsNoOp(t *testing.T) {
	for _, mode := range []string{MergeModeFFOnly, MergeModeCommit} {
		t.Run(mode, func(t *testing.T) {
			f := newMergeFx(t)
			head := f.branch("feat", f.base, "a.txt", "a\n")
			first, err := Merge(context.Background(), f.req("feat", head, mode))
			if err != nil {
				t.Fatal(err)
			}
			second, err := Merge(context.Background(), f.req("feat", head, mode))
			if err != nil {
				t.Fatal(err)
			}
			want := MergeResult{
				MergedSHA: first.MergedSHA, TargetBefore: first.TargetAfter, TargetAfter: first.TargetAfter,
				AlreadyMerged: true,
			}
			if second != want {
				t.Fatalf("second = %+v, want %+v", second, want)
			}
			if got := f.originMain(); got != first.TargetAfter {
				t.Fatalf("origin main = %s", got)
			}
		})
	}
}

func TestMergeNeverRunsHooks(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	f.advanceOrigin("o.txt", "o\n")
	marker := filepath.Join(f.root, "hook-ran")
	hooks := filepath.Join(f.repo, ".git", "hooks")
	for _, h := range []string{"post-checkout", "pre-merge-commit", "post-merge", "pre-push", "reference-transaction"} {
		script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(hooks, h), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Merge(context.Background(), f.req("feat", head, MergeModeCommit)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a git hook ran")
	}
}

func TestMergeRejectsBadRequests(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	cases := map[string]func(r *MergeRequest){
		"mode":            func(r *MergeRequest) { r.Mode = "squash" },
		"short sha":       func(r *MergeRequest) { r.HeadSHA = head[:10] },
		"option as sha":   func(r *MergeRequest) { r.HeadSHA = "--upload-pack=x" },
		"option as ref":   func(r *MergeRequest) { r.TargetBranch = "--force" },
		"bad ref":         func(r *MergeRequest) { r.ChangeBranch = "a..b" },
		"no identity":     func(r *MergeRequest) { r.Mode = MergeModeCommit; r.Identity = MergeIdentity{} },
		"worktree = repo": func(r *MergeRequest) { r.WorktreeDir = f.repo },
		"no dirs":         func(r *MergeRequest) { r.RepoDir = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := f.req("feat", head, MergeModeFFOnly)
			mutate(&r)
			if _, err := Merge(context.Background(), r); err == nil {
				t.Fatal("expected an error")
			}
			if got := f.originMain(); got != f.base {
				t.Fatalf("origin changed to %s", got)
			}
		})
	}
}

func TestMergeRefusesForeignDirectory(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	if err := os.MkdirAll(f.wt, 0o755); err != nil {
		t.Fatal(err)
	}
	f.write(f.wt, "precious.txt", "keep\n")
	if _, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly)); err == nil {
		t.Fatal("expected an error for a non-worktree directory")
	}
	if _, err := os.Stat(filepath.Join(f.wt, "precious.txt")); err != nil {
		t.Fatal("foreign file was touched")
	}
}

func TestMergeRecreatesDeletedWorktree(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "one.txt", "1\n")
	req := f.req("one", h1, MergeModeFFOnly)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.wt); err != nil {
		t.Fatal(err)
	}
	h2 := f.branch("two", f.base, "two.txt", "2\n")
	if _, err := Merge(context.Background(), f.req("two", h2, MergeModeFFOnly)); err != nil {
		t.Fatal(err)
	}
}
