package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommitWorkerEndWIP(t *testing.T) {
	repo := initWIPRepo(t)
	branch := gitOutT(t, repo, "branch", "--show-current")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "large.bin"), make([]byte, 5_000_001), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Make the ignore rule part of the baseline so only worker changes are tested.
	gitRunT(t, repo, "add", ".gitignore")
	gitRunT(t, repo, "commit", "-m", "ignore rule")
	got := commitWorkerEndWIP(context.Background(), repo, branch, "stopped")
	if got.Failed != "" {
		t.Fatal(got.Failed)
	}
	if got.SHA == "" || got.Files != 2 || !strings.Contains(got.Skipped, "large.bin") || !strings.Contains(got.Skipped, "binary.dat") || !strings.Contains(got.noticeDetail(), "left uncommitted changes; committed as WIP ") || !strings.Contains(got.noticeDetail(), ": 2 files") {
		t.Fatalf("unexpected commit result: %+v", got)
	}
	if gitOutT(t, repo, "show", "-s", "--format=%an <%ae>") != "agent-net <agent-net@localhost>" {
		t.Fatal("wrong WIP author")
	}
	if gitOutT(t, repo, "show", "-s", "--format=%B") != "WIP: uncommitted changes at worker end (stopped)" {
		t.Fatal("wrong WIP subject")
	}
	if strings.Contains(gitOutT(t, repo, "show", "--format=", "--name-only", "HEAD"), "ignored.txt") {
		t.Fatal("ignored file committed")
	}
}

func TestCommitWorkerEndWIPEmptyDirDoesNothing(t *testing.T) {
	got := commitWorkerEndWIP(context.Background(), "", "", "lost")
	if got.Failed != "" || got.SHA != "" {
		t.Fatalf("a WIP save without a worktree did work: %+v", got)
	}
}

func TestCommitWorkerEndWIPClean(t *testing.T) {
	repo := initWIPRepo(t)
	before := gitOutT(t, repo, "rev-parse", "HEAD")
	got := commitWorkerEndWIP(context.Background(), repo, gitOutT(t, repo, "branch", "--show-current"), "stopped")
	if got.SHA != "" || got.Files != 0 || got.Failed != "" {
		t.Fatalf("unexpected commit result: %+v", got)
	}
	if gitOutT(t, repo, "rev-parse", "HEAD") != before {
		t.Fatal("clean repo got a commit")
	}
}

func TestCommitWorkerEndWIPFailureLeavesFiles(t *testing.T) {
	repo := initWIPRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte("change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "index.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got := commitWorkerEndWIP(context.Background(), repo, gitOutT(t, repo, "branch", "--show-current"), "failed")
	if got.Failed == "" {
		t.Fatal("expected staging failure")
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "change.txt")); string(got) != "change\n" {
		t.Fatal("working file changed")
	}
}

// A stale empty index lock in the worker's own worktree is removed, and the
// WIP save is retried once.
func TestCommitWorkerEndWIPRemovesStaleEmptyIndexLock(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "worktrees", "w_test")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, repo, "init", "-b", "worker")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, repo, "add", "README.md")
	gitRunT(t, repo, "commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-workerIndexLockStaleAfter - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	var logs []string
	got := commitWorkerEndWIPWithPolicy(context.Background(), repo, "worker", "lost", wipLockPolicy{
		WorktreesRoot: filepath.Join(root, "worktrees"),
		Logf:          func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if got.Failed != "" || got.SHA == "" {
		t.Fatalf("WIP save with a stale lock: %+v", got)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the stale index lock was left in place: %v", err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "removed stale index lock") {
		t.Fatalf("the removal was not logged: %v", logs)
	}
}

// A fresh lock may belong to a running git process, so it is left alone.
func TestCommitWorkerEndWIPLeavesFreshIndexLock(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "worktrees", "w_test")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, repo, "init", "-b", "worker")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, repo, "add", "README.md")
	gitRunT(t, repo, "commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := commitWorkerEndWIPWithPolicy(context.Background(), repo, "worker", "lost", wipLockPolicy{
		WorktreesRoot: filepath.Join(root, "worktrees"),
		Logf:          func(string, ...any) {},
	})
	if got.Failed == "" || !strings.Contains(got.Failed, "index.lock") {
		t.Fatalf("a fresh lock was not reported: %+v", got)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("the fresh index lock was removed: %v", err)
	}
}

func TestWorkerEndCommitResultControlsWorktreeRetention(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  workerEndCommitResult
		keep bool
	}{
		{name: "successful commit", got: workerEndCommitResult{SHA: "abc123", Files: 1}},
		{name: "clean", got: workerEndCommitResult{}},
		{name: "failed", got: workerEndCommitResult{Failed: "git commit failed"}, keep: true},
		{name: "merge in progress", got: workerEndCommitResult{Skipped: "git operation in progress"}, keep: true},
		{name: "excluded files remain", got: workerEndCommitResult{SHA: "abc123", Files: 1, Skipped: "large.bin"}, keep: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got.keepWorktree(); got != tc.keep {
				t.Fatalf("keepWorktree() = %t, want %t", got, tc.keep)
			}
		})
	}
}

func initWIPRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRunT(t, repo, "init", "-b", "worker")
	gitRunT(t, repo, "config", "user.name", "Owner")
	gitRunT(t, repo, "config", "user.email", "owner@example.test")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, repo, "add", "tracked.txt")
	gitRunT(t, repo, "commit", "-m", "base")
	return repo
}

func gitRunT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
