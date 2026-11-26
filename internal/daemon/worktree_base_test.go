package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// baseTestRepo makes a project whose branch main tracks origin/main, where
// origin is a local bare repo. It returns the project, the bare repo and a
// second clone that stands in for "work landed by PR on the remote".
func baseTestRepo(t *testing.T) (repo, bare, other string) {
	t.Helper()
	repo = newRepo(t, "base-project")
	root := filepath.Dir(repo)
	bare = filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	other = filepath.Join(root, "other")
	gitIn(t, root, "clone", "-q", bare, other)
	return repo, bare, other
}

func baseTestCommit(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", name)
	out, err := gitOut(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func baseTestWorker(t *testing.T, repo string) (*Daemon, *worker, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}, log: log.New(&logs, "", 0)}
	w := &worker{id: protocol.NewID("w"), harness: "codex", project: "base-project", path: repo}
	return d, w, &logs
}

func baseTestHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitOut(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorktreeBaseStartsAtUpstreamTip(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	localBefore := baseTestHead(t, repo)
	tip := baseTestCommit(t, other, "landed-by-pr.txt")
	gitIn(t, other, "push", "-q", "origin", "main")

	d, w, _ := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != tip {
		t.Fatalf("worktree starts at %s, want origin tip %s", got, tip)
	}
	if w.base != tip {
		t.Fatalf("worker base %s, want %s", w.base, tip)
	}
	// The owner's checkout is untouched.
	if got := baseTestHead(t, repo); got != localBefore {
		t.Fatalf("local main moved to %s, was %s", got, localBefore)
	}
	if _, err := os.Stat(filepath.Join(repo, "landed-by-pr.txt")); err == nil {
		t.Fatal("the owner's checkout was changed")
	}
}

// The owner's commits on the default branch that origin lacks reach the worker,
// because the checkout is that branch and strictly ahead of origin.
func TestWorktreeBaseUnpushedCommitKeepsLocalHead(t *testing.T) {
	repo, _, _ := baseTestRepo(t)
	local := baseTestCommit(t, repo, "unpushed.txt")

	d, w, logs := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != local {
		t.Fatalf("worktree starts at %s, want local head %s", got, local)
	}
	if w.base != local {
		t.Fatalf("worker base %s, want %s", w.base, local)
	}
	if !strings.Contains(logs.String(), "worktree base") {
		t.Fatalf("no log line that says why: %q", logs.String())
	}
}

func TestWorktreeBaseNoUpstreamKeepsLocalHead(t *testing.T) {
	repo := newRepo(t, "base-project")
	local := baseTestHead(t, repo)

	d, w, logs := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != local || w.base != local {
		t.Fatalf("worktree starts at %s, base %s, want %s", got, w.base, local)
	}
	if !strings.Contains(logs.String(), "worktree base") {
		t.Fatalf("no log line that says why: %q", logs.String())
	}
}

func TestWorktreeBaseFetchFailureKeepsLocalHead(t *testing.T) {
	repo, bare, _ := baseTestRepo(t)
	local := baseTestHead(t, repo)
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}

	d, w, logs := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatalf("the worker must still start: %v", err)
	}
	if got := baseTestHead(t, w.worktree); got != local || w.base != local {
		t.Fatalf("worktree starts at %s, base %s, want %s", got, w.base, local)
	}
	if !strings.Contains(logs.String(), "worktree base") {
		t.Fatalf("no log line that says why: %q", logs.String())
	}
}

func TestWorktreeBaseRequestedBaseIgnoresUpstream(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	baseTestCommit(t, other, "remote.txt")
	gitIn(t, other, "push", "-q", "origin", "main")
	local := baseTestHead(t, repo)

	d, w, _ := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w, "main"); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != local {
		t.Fatalf("a requested base must stay local: got %s want %s", got, local)
	}
}

// A worker starts from origin's default branch even when the owner's checkout
// is on another branch: an upgrade or a merge lands on the remote, and the
// local branch the owner left checked out stays behind.
func TestWorktreeBaseStartsAtOriginDefaultBranch(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	gitIn(t, repo, "checkout", "-q", "-b", "topic")
	gitIn(t, repo, "push", "-q", "-u", "origin", "topic")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	tip := baseTestCommit(t, other, "landed-by-pr.txt")
	gitIn(t, other, "push", "-q", "origin", "main")

	d, w, _ := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != tip {
		t.Fatalf("worktree starts at %s, want origin/main tip %s", got, tip)
	}
	if w.base != tip {
		t.Fatalf("worker base %s, want %s", w.base, tip)
	}
}

// A checkout on a side branch with its own commit must not hold the worker
// back: the worker starts from origin's default branch.
func TestWorktreeBaseSideBranchStartsAtOriginDefault(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	gitIn(t, repo, "checkout", "-q", "-b", "side")
	baseTestCommit(t, repo, "side-work.txt")
	tip := baseTestCommit(t, other, "landed-by-pr.txt")
	gitIn(t, other, "push", "-q", "origin", "main")

	d, w, logs := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != tip || w.base != tip {
		t.Fatalf("worktree starts at %s, base %s, want origin/main tip %s", got, w.base, tip)
	}
	if !strings.Contains(logs.String(), "on branch side") {
		t.Fatalf("the log does not say why the local head was left out: %q", logs.String())
	}
}

// A local main with a commit origin lacks, while origin has moved on, is
// diverged. The worker starts from origin's default branch.
func TestWorktreeBaseDivergedMainStartsAtOriginDefault(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	baseTestCommit(t, repo, "unpushed.txt")
	tip := baseTestCommit(t, other, "landed-by-pr.txt")
	gitIn(t, other, "push", "-q", "origin", "main")

	d, w, logs := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != tip || w.base != tip {
		t.Fatalf("worktree starts at %s, base %s, want origin/main tip %s", got, w.base, tip)
	}
	if !strings.Contains(logs.String(), "diverged") {
		t.Fatalf("the log does not say why the local head was left out: %q", logs.String())
	}
}

// A detached checkout is not the default branch, so it does not keep its
// unpushed commit as the base either.
func TestWorktreeBaseDetachedHeadStartsAtOriginDefault(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	gitIn(t, repo, "checkout", "-q", "--detach")
	baseTestCommit(t, repo, "detached-work.txt")
	tip := baseTestCommit(t, other, "landed-by-pr.txt")
	gitIn(t, other, "push", "-q", "origin", "main")

	d, w, _ := baseTestWorker(t, repo)
	if err := d.createWorktree(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := baseTestHead(t, w.worktree); got != tip || w.base != tip {
		t.Fatalf("worktree starts at %s, base %s, want origin/main tip %s", got, w.base, tip)
	}
}

// A config.lock that git left behind must not stop worktree creation or the
// branch upstream write. An empty lock older than ten seconds is stale; a
// fresh one or one that holds content belongs to a running process.
func TestWorktreeStaleConfigLockRemoved(t *testing.T) {
	writeLock := func(t *testing.T, repo, content string, age time.Duration) string {
		t.Helper()
		path := filepath.Join(repo, ".git", "config.lock")
		if err := os.WriteFile(path, []byte(content), 0o444); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("stale empty lock is removed", func(t *testing.T) {
		repo, _, _ := baseTestRepo(t)
		lock := writeLock(t, repo, "", time.Minute)

		d, w, logs := baseTestWorker(t, repo)
		if err := d.createWorktree(context.Background(), w); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(lock); !os.IsNotExist(err) {
			t.Fatalf("stale lock still there: %v", err)
		}
		if !strings.Contains(logs.String(), "worker.git stale config.lock removed path="+lock) {
			t.Fatalf("no removal log line: %q", logs.String())
		}
		// The upstream write needs the config lock, so this proves it went through.
		out, err := gitOut(context.Background(), repo, "config", "--get", "branch."+w.branch+".remote")
		if err != nil || out != "origin" {
			t.Fatalf("branch upstream is %q (%v), want origin", out, err)
		}
	})

	for _, tc := range []struct {
		name    string
		content string
		age     time.Duration
	}{
		{"fresh lock is kept", "", 0},
		{"old lock with content is kept", "pid 123", time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _, _ := baseTestRepo(t)
			lock := writeLock(t, repo, tc.content, tc.age)

			d, w, logs := baseTestWorker(t, repo)
			if err := d.createWorktree(context.Background(), w); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(lock); err != nil {
				t.Fatalf("lock was removed: %v", err)
			}
			if strings.Contains(logs.String(), "stale config.lock removed") {
				t.Fatalf("log claims a removal: %q", logs.String())
			}
		})
	}
}
