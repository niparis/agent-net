package delivery

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	mirror string
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
		mirror: filepath.Join(root, "merge-mirror"),
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
		RepoDir: f.repo, MirrorDir: f.mirror, WorktreeDir: f.wt, Remote: "origin", RemoteURL: f.origin,
		TargetBranch: "main", ChangeBranch: branch, BaseSHA: f.base, HeadSHA: head, Mode: mode,
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
	head := f.branch("gone", f.base, "a.txt", "a\n")
	mergeGit(t, f.repo, "branch", "-q", "-D", "gone")
	_, err := Merge(context.Background(), f.req("gone", head, MergeModeFFOnly))
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

// S7: the merge worktree belongs to Merge. Whatever a stopped run or a
// stray process left in it is cleaned at the start of the next call.
func TestMergeCleansOwnedWorktree(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "one.txt", "1\n")
	req := f.req("one", h1, MergeModeFFOnly)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	h2 := f.branch("two", f.base, "two.txt", "2\n")

	f.write(f.wt, "scratch.txt", "x\n")
	f.write(f.wt, "README", "edited\n")
	f.write(f.wt, "one.txt", "staged\n")
	mergeGit(t, f.wt, "add", "one.txt")
	if err := os.MkdirAll(filepath.Join(f.wt, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.write(f.wt, "build/out.o", "x\n")

	res, err := Merge(context.Background(), f.req("two", h2, MergeModeCommit))
	if err != nil {
		t.Fatal(err)
	}
	f.assertClean()
	for _, n := range []string{"scratch.txt", "build/out.o", "one.txt"} {
		if _, err := os.Stat(filepath.Join(f.wt, n)); err == nil {
			t.Errorf("%s survived the cleanup", n)
		}
	}
	if got := mergeGit(t, f.origin, "show", res.MergedSHA+":README"); got != "base" {
		t.Fatalf("README in the merge = %q: the stray edit was merged", got)
	}
}

// S7: a run that stopped in the middle of a conflict left MERGE_HEAD behind.
func TestMergeRecoversInterruptedMerge(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "README", "one\n")
	req := f.req("one", h1, MergeModeFFOnly)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	other := f.branch("other", f.base, "README", "other\n")
	// The merge worktree belongs to the mirror, which has not seen "other".
	mergeGit(t, f.mirror, "fetch", "-q", f.repo, "+refs/heads/other:refs/heads/other")
	cmd := exec.Command("git", "-c", "user.name=T", "-c", "user.email=t@example.com", "merge", "--no-ff", "-q", "-m", "x", other)
	cmd.Dir = f.wt
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("control: the merge did not conflict:\n%s", out)
	}
	mergeGit(t, f.wt, "rev-parse", "-q", "--verify", "MERGE_HEAD")

	h2 := f.branch("two", f.base, "two.txt", "2\n")
	if _, err := Merge(context.Background(), f.req("two", h2, MergeModeCommit)); err != nil {
		t.Fatal(err)
	}
	f.assertClean()
}

// S7: the cleanup after a conflict runs even when the caller's context ended.
func TestMergeConflictCleanupSurvivesCancel(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "README", "feature side\n")
	f.advanceOrigin("README", "main side\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &mergeRunner{afterMerge: cancel}
	if _, err := m.merge(ctx, f.req("feat", head, MergeModeCommit)); err == nil {
		t.Fatal("expected an error")
	}
	f.assertClean()
}

// S4: Merge works only in a worktree it created.
func TestMergeRefusesUnmarkedWorktree(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	mergeGit(t, f.repo, "worktree", "add", "-q", "--detach", f.wt, f.base)
	f.write(f.wt, "precious.txt", "keep\n")

	_, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if !errors.Is(err, ErrNotOwnedWorktree) {
		t.Fatalf("err = %v, want ErrNotOwnedWorktree", err)
	}
	if _, err := os.Stat(filepath.Join(f.wt, "precious.txt")); err != nil {
		t.Fatal("a file in a worktree Merge does not own was removed")
	}
	if got := f.originMain(); got != f.base {
		t.Fatalf("origin moved to %s", got)
	}
}

func TestMergeRefusesWorktreeOnBranch(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "one.txt", "1\n")
	req := f.req("one", h1, MergeModeFFOnly)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	mergeGit(t, f.wt, "checkout", "-q", "-b", "someones-work")
	f.write(f.wt, "precious.txt", "keep\n")
	h2 := f.branch("two", f.base, "two.txt", "2\n")

	_, err := Merge(context.Background(), f.req("two", h2, MergeModeFFOnly))
	if !errors.Is(err, ErrNotOwnedWorktree) {
		t.Fatalf("err = %v, want ErrNotOwnedWorktree", err)
	}
	if _, err := os.Stat(filepath.Join(f.wt, "precious.txt")); err != nil {
		t.Fatal("a file on a branch checkout was removed")
	}
}

func TestMergeRefusesSymlinkWorktree(t *testing.T) {
	f := newMergeFx(t)
	h1 := f.branch("one", f.base, "one.txt", "1\n")
	req := f.req("one", h1, MergeModeFFOnly)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	h2 := f.branch("two", f.base, "two.txt", "2\n")
	link := filepath.Join(f.root, "link-wt")
	if err := os.Symlink(f.wt, link); err != nil {
		t.Fatal(err)
	}
	r := f.req("two", h2, MergeModeFFOnly)
	r.WorktreeDir = link
	if _, err := Merge(context.Background(), r); !errors.Is(err, ErrNotOwnedWorktree) {
		t.Fatalf("err = %v, want ErrNotOwnedWorktree", err)
	}
	if got := f.originMain(); got != f.base {
		t.Fatalf("origin moved to %s", got)
	}
}

// S6: one Merge at a time per worktree.
func TestMergeLockBlocksSecondMerge(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	lf, err := os.OpenFile(f.wt+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	req := f.req("feat", head, MergeModeFFOnly)
	req.Timeout = 300 * time.Millisecond
	if _, err := Merge(context.Background(), req); !errors.Is(err, ErrMergeBusy) {
		t.Fatalf("err = %v, want ErrMergeBusy", err)
	}
	if got := f.originMain(); got != f.base {
		t.Fatalf("origin moved to %s while another merge held the lock", got)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	req.Timeout = 30 * time.Second
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

// S5: the push is a compare-and-swap on the remote tip the merge started
// from. A remote rewound to an older commit must not be fast-forwarded past.
func TestMergePushIsCompareAndSwap(t *testing.T) {
	f := newMergeFx(t)
	moved := f.advanceOrigin("o.txt", "o\n")
	mergeGit(t, f.repo, "fetch", "-q", "origin")
	head := f.branch("feat", "origin/main", "a.txt", "a\n")

	m := &mergeRunner{beforePush: func() { mergeGit(t, f.origin, "update-ref", "refs/heads/main", f.base) }}
	_, err := m.merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("err = %v, want ErrRemoteMoved", err)
	}
	if got := f.originMain(); got != f.base {
		t.Fatalf("origin main = %s, want the rewound %s (merge started from %s)", got, f.base, moved)
	}
}

// S5: the tip comes from the remote, not from the local ref store, which a
// worker can write.
func TestMergeTipComesFromRemote(t *testing.T) {
	f := newMergeFx(t)
	moved := f.advanceOrigin("o.txt", "o\n")
	mergeGit(t, f.repo, "fetch", "-q", "origin")
	head := f.branch("feat", "origin/main", "a.txt", "a\n")

	m := &mergeRunner{afterFetch: func() { mergeGit(t, f.repo, "update-ref", "refs/remotes/origin/main", f.base) }}
	res, err := m.merge(context.Background(), f.req("feat", head, MergeModeFFOnly))
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetBefore != moved {
		t.Fatalf("TargetBefore = %s, want the remote tip %s", res.TargetBefore, moved)
	}
	if got := f.originMain(); got != head {
		t.Fatalf("origin main = %s", got)
	}
}

// S8: a change whose head is its base has nothing to merge.
func TestMergeRefusesEmptyChange(t *testing.T) {
	f := newMergeFx(t)
	mergeGit(t, f.repo, "branch", "feat", f.base)
	_, err := Merge(context.Background(), f.req("feat", f.base, MergeModeFFOnly))
	if !errors.Is(err, ErrEmptyChange) {
		t.Fatalf("err = %v, want ErrEmptyChange", err)
	}
}

// S9: a missing target branch (a release branch) is created from CreateFrom.
func TestMergeCreatesMissingTarget(t *testing.T) {
	for _, mode := range []string{MergeModeFFOnly, MergeModeCommit} {
		t.Run(mode, func(t *testing.T) {
			f := newMergeFx(t)
			head := f.branch("feat", f.base, "a.txt", "a\n")
			req := f.req("feat", head, mode)
			req.TargetBranch = "release/1"
			req.CreateFrom = f.base
			res, err := Merge(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Created || !res.Pushed || res.TargetBefore != f.base || res.AlreadyMerged {
				t.Fatalf("result = %+v", res)
			}
			if got := mergeGit(t, f.origin, "rev-parse", "refs/heads/release/1"); got != res.TargetAfter {
				t.Fatalf("release/1 = %s, want %s", got, res.TargetAfter)
			}
			if got := f.originMain(); got != f.base {
				t.Fatalf("main moved to %s", got)
			}
			// The second call merges into the branch that now exists.
			again, err := Merge(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !again.AlreadyMerged || again.Created {
				t.Fatalf("second result = %+v", again)
			}
		})
	}
}

func TestMergeTargetMissingWithoutCreateFrom(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	req := f.req("feat", head, MergeModeFFOnly)
	req.TargetBranch = "release/1"
	if _, err := Merge(context.Background(), req); !errors.Is(err, ErrTargetMissing) {
		t.Fatalf("err = %v, want ErrTargetMissing", err)
	}
}

// S9: the branch is created with a lease that it does not exist, so a branch
// someone else created in the meantime is not overwritten.
func TestMergeCreateLosesRace(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	theirs := f.advanceOrigin("o.txt", "o\n")
	req := f.req("feat", head, MergeModeFFOnly)
	req.TargetBranch = "release/1"
	req.CreateFrom = f.base
	m := &mergeRunner{beforePush: func() { mergeGit(t, f.origin, "update-ref", "refs/heads/release/1", theirs) }}
	if _, err := m.merge(context.Background(), req); !errors.Is(err, ErrRemoteMoved) {
		t.Fatalf("err = %v, want ErrRemoteMoved", err)
	}
	if got := mergeGit(t, f.origin, "rev-parse", "refs/heads/release/1"); got != theirs {
		t.Fatalf("release/1 = %s, want theirs %s", got, theirs)
	}
}

func TestMergeCreateFromIgnoredWhenTargetExists(t *testing.T) {
	f := newMergeFx(t)
	head := f.branch("feat", f.base, "a.txt", "a\n")
	req := f.req("feat", head, MergeModeFFOnly)
	req.CreateFrom = head
	res, err := Merge(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.TargetBefore != f.base {
		t.Fatalf("result = %+v", res)
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
		"no base":         func(r *MergeRequest) { r.BaseSHA = "" },
		"short base":      func(r *MergeRequest) { r.BaseSHA = f.base[:10] },
		"bad create-from": func(r *MergeRequest) { r.CreateFrom = "main" },
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
	if _, err := Merge(context.Background(), f.req("feat", head, MergeModeFFOnly)); !errors.Is(err, ErrNotOwnedWorktree) {
		t.Fatalf("err = %v, want ErrNotOwnedWorktree", err)
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

func newMergeFxSHA256(t *testing.T) *mergeFx {
	t.Helper()
	root := t.TempDir()
	f := &mergeFx{t: t, root: root,
		origin: filepath.Join(root, "origin.git"),
		repo:   filepath.Join(root, "repo"),
		wt:     filepath.Join(root, "merge-wt"),
		mirror: filepath.Join(root, "merge-mirror"),
	}
	mergeGit(t, root, "init", "--bare", "-b", "main", "--object-format=sha256", f.origin)
	mergeGit(t, root, "clone", "-q", f.origin, f.repo)
	mergeGit(t, f.repo, "checkout", "-q", "-B", "main")
	f.write(f.repo, "README", "base\n")
	mergeGit(t, f.repo, "add", ".")
	mergeGit(t, f.repo, "commit", "-q", "-m", "base")
	mergeGit(t, f.repo, "push", "-q", "-u", "origin", "main")
	f.base = mergeGit(t, f.repo, "rev-parse", "HEAD")
	return f
}

func TestMergeSHA256Repository(t *testing.T) {
	f := newMergeFxSHA256(t)
	head := f.branch("feat", f.base, "feat.txt", "feature\n")
	res, err := Merge(context.Background(), f.req("feat", head, MergeModeCommit))
	if err != nil {
		t.Fatalf("Merge SHA256: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected push")
	}

	// Refuse when mirror's object format differs from RepoDir's.
	mismatchedRoot := t.TempDir()
	badMirror := filepath.Join(mismatchedRoot, "bad-mirror")
	mergeGit(t, mismatchedRoot, "init", "--bare", "--object-format=sha1", badMirror)
	if err := os.WriteFile(filepath.Join(badMirror, mergeMirrorMarker), []byte(badMirror+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := f.req("feat", head, MergeModeCommit)
	req.MirrorDir = badMirror
	if _, err := Merge(context.Background(), req); !errors.Is(err, ErrNotOwnedMirror) {
		t.Fatalf("expected ErrNotOwnedMirror for mismatched object-format, got %v", err)
	}
}
