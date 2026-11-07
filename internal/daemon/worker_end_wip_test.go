package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	got, err := commitWorkerEndWIP(context.Background(), repo, branch, "stopped")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "left uncommitted changes; committed as WIP ") || !strings.Contains(got, ": 2 files") || !strings.Contains(got, "large.bin") || !strings.Contains(got, "binary.dat") {
		t.Fatalf("unexpected notice detail: %q", got)
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

func TestCommitWorkerEndWIPClean(t *testing.T) {
	repo := initWIPRepo(t)
	before := gitOutT(t, repo, "rev-parse", "HEAD")
	got, err := commitWorkerEndWIP(context.Background(), repo, gitOutT(t, repo, "branch", "--show-current"), "stopped")
	if err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
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
	_, err := commitWorkerEndWIP(context.Background(), repo, gitOutT(t, repo, "branch", "--show-current"), "failed")
	if err == nil {
		t.Fatal("expected branch validation error")
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "change.txt")); string(got) != "change\n" {
		t.Fatal("working file changed")
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
