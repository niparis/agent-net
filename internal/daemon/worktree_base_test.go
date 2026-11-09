package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestWorktreeBaseUnpushedCommitKeepsLocalHead(t *testing.T) {
	repo, _, other := baseTestRepo(t)
	baseTestCommit(t, other, "remote.txt")
	gitIn(t, other, "push", "-q", "origin", "main")
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
