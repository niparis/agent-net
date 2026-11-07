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
)

// cleanupRemoteRepo makes a repository that pushes to a bare origin, plus a
// second clone used to change the origin's default branch.
func cleanupRemoteRepo(t *testing.T, name string) (repo, bare, peer string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bare = filepath.Join(base, "origin.git")
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", bare)
	repo = filepath.Join(base, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "README.md")
	gitIn(t, repo, "commit", "-q", "-m", "first")
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	peer = filepath.Join(base, "peer")
	gitIn(t, base, "clone", "-q", bare, peer)
	return repo, bare, peer
}

// cleanupAddWorktree adds an agent-net worker worktree and returns its folder.
func cleanupAddWorktree(t *testing.T, repo, state, id string) string {
	t.Helper()
	dir := filepath.Join(state, "worktrees", id)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "worktree", "add", "-q", "-b", "agent-net/"+id, dir)
	return dir
}

// cleanupCommit writes a file in dir and commits it, returning the commit.
func cleanupCommit(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", content)
	return gitHead(t, dir, "HEAD")
}

// cleanupCandidate is an ended worker worktree with no delivery change, the
// case of a project that does not use the delivery pipeline.
func cleanupCandidate(id, repo, dir string) cleanupWorktree {
	return cleanupWorktree{ID: id, Project: repo, Path: dir, Branch: "agent-net/" + id,
		EndedAt: time.Now(), ChangeChecked: true}
}

func cleanupRun(t *testing.T, state string, w cleanupWorktree) string {
	t.Helper()
	var logs bytes.Buffer
	d := &Daemon{cfg: Config{StateDir: state}, log: log.New(&logs, "", 0), workers: map[string]*worker{}}
	if err := d.cleanupWorktree(context.Background(), w, time.Now()); err != nil {
		t.Fatal(err)
	}
	return logs.String()
}

func cleanupKept(t *testing.T, dir, branch, repo string) {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("worktree removed: %v", err)
	}
	if _, err := gitOut(context.Background(), repo, "show-ref", "--verify", "refs/heads/"+branch); err != nil {
		t.Fatalf("branch removed: %v", err)
	}
}

func cleanupGone(t *testing.T, dir, branch, repo string) {
	t.Helper()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	if _, err := gitOut(context.Background(), repo, "show-ref", "--verify", "refs/heads/"+branch); err == nil {
		t.Fatalf("branch remains: %s", branch)
	}
}

// A branch merged on the origin but not yet fetched is removed after cleanup
// fetches the default branch. This is the case of the Mac's sovl-clone.
func TestCleanupWorktreeMergedOnRemoteNotFetched(t *testing.T) {
	t.Parallel()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_remote")
	head := cleanupCommit(t, dir, "change", "change")
	gitIn(t, repo, "push", "-q", "origin", "agent-net/w_remote")
	gitIn(t, peer, "fetch", "-q", "origin")
	gitIn(t, peer, "merge", "-q", "--no-ff", "-m", "merge", "origin/agent-net/w_remote")
	gitIn(t, peer, "push", "-q", "origin", "main")

	// The local default branch and the stale origin ref do not contain it.
	if _, err := gitOut(context.Background(), repo, "merge-base", "--is-ancestor", head, "refs/heads/main"); err == nil {
		t.Fatal("local main already contains the worker commit")
	}
	if _, err := gitOut(context.Background(), repo, "merge-base", "--is-ancestor", head, "refs/remotes/origin/main"); err == nil {
		t.Fatal("origin/main was already fetched")
	}

	logs := cleanupRun(t, state, cleanupCandidate("w_remote", repo, dir))
	cleanupGone(t, dir, "agent-net/w_remote", repo)
	if !strings.Contains(logs, "reason=merged-remote") {
		t.Fatalf("log: %q", logs)
	}
}

// A squash merge on the origin of a multi-commit branch is removed.
func TestCleanupWorktreeSquashMergedOnRemote(t *testing.T) {
	t.Parallel()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_squash")
	cleanupCommit(t, dir, "one", "one")
	cleanupCommit(t, dir, "two", "two")
	gitIn(t, repo, "push", "-q", "origin", "agent-net/w_squash")
	gitIn(t, peer, "fetch", "-q", "origin", "agent-net/w_squash")
	gitIn(t, peer, "merge", "-q", "--squash", "FETCH_HEAD")
	gitIn(t, peer, "commit", "-q", "-m", "squash")
	gitIn(t, peer, "push", "-q", "origin", "main")

	logs := cleanupRun(t, state, cleanupCandidate("w_squash", repo, dir))
	cleanupGone(t, dir, "agent-net/w_squash", repo)
	if !strings.Contains(logs, "reason=merged-squash") {
		t.Fatalf("log: %q", logs)
	}
}

// A cherry-picked worker commit on the origin is removed.
func TestCleanupWorktreeCherryPickedOnRemote(t *testing.T) {
	t.Parallel()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_cherry")
	head := cleanupCommit(t, dir, "change", "change")
	gitIn(t, repo, "push", "-q", "origin", "agent-net/w_cherry")
	gitIn(t, peer, "fetch", "-q", "origin", "agent-net/w_cherry")
	gitIn(t, peer, "cherry-pick", "--no-commit", head)
	gitIn(t, peer, "commit", "-q", "-m", "picked")
	gitIn(t, peer, "push", "-q", "origin", "main")

	logs := cleanupRun(t, state, cleanupCandidate("w_cherry", repo, dir))
	cleanupGone(t, dir, "agent-net/w_cherry", repo)
	if !strings.Contains(logs, "reason=merged-cherry") {
		t.Fatalf("log: %q", logs)
	}
}

// Two worker commits, one cherry-picked on the origin: the branch is not fully
// merged and is kept.
func TestCleanupWorktreeKeepsPartialCherryPick(t *testing.T) {
	t.Parallel()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_partial")
	first := cleanupCommit(t, dir, "one", "one")
	cleanupCommit(t, dir, "two", "two")
	gitIn(t, repo, "push", "-q", "origin", "agent-net/w_partial")
	gitIn(t, peer, "fetch", "-q", "origin", "agent-net/w_partial")
	gitIn(t, peer, "cherry-pick", "--no-commit", first)
	gitIn(t, peer, "commit", "-q", "-m", "picked")
	gitIn(t, peer, "push", "-q", "origin", "main")

	logs := cleanupRun(t, state, cleanupCandidate("w_partial", repo, dir))
	cleanupKept(t, dir, "agent-net/w_partial", repo)
	if strings.Contains(logs, "reason=merged") {
		t.Fatalf("log: %q", logs)
	}
}

// An unrelated commit on the origin has a different tree: the branch is kept.
func TestCleanupWorktreeKeepsUnrelatedRemoteCommit(t *testing.T) {
	t.Parallel()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_unrelated")
	cleanupCommit(t, dir, "change", "change")
	if err := os.WriteFile(filepath.Join(peer, "README.md"), []byte("# changed elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, peer, "add", "README.md")
	gitIn(t, peer, "commit", "-q", "-m", "unrelated")
	gitIn(t, peer, "push", "-q", "origin", "main")

	logs := cleanupRun(t, state, cleanupCandidate("w_unrelated", repo, dir))
	cleanupKept(t, dir, "agent-net/w_unrelated", repo)
	if strings.Contains(logs, "reason=merged") {
		t.Fatalf("log: %q", logs)
	}
}

// A dirty worktree is never removed, even when the origin has its commit.
func TestCleanupWorktreeKeepsDirtyMergedOnRemote(t *testing.T) {
	t.Parallel()
	repo, _, peer := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_dirty")
	cleanupCommit(t, dir, "change", "change")
	gitIn(t, repo, "push", "-q", "origin", "agent-net/w_dirty")
	gitIn(t, peer, "fetch", "-q", "origin")
	gitIn(t, peer, "merge", "-q", "--no-ff", "-m", "merge", "origin/agent-net/w_dirty")
	gitIn(t, peer, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(dir, "dirty"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupRun(t, state, cleanupCandidate("w_dirty", repo, dir))
	cleanupKept(t, dir, "agent-net/w_dirty", repo)
}

// A failed fetch is not proof of a merge: it leaves the worktree in place.
func TestCleanupWorktreeFetchFailureKeepsBranch(t *testing.T) {
	t.Parallel()
	repo, _, _ := cleanupRemoteRepo(t, "project")
	state := t.TempDir()
	dir := cleanupAddWorktree(t, repo, state, "w_offline")
	cleanupCommit(t, dir, "change", "change")
	gitIn(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))

	logs := cleanupRun(t, state, cleanupCandidate("w_offline", repo, dir))
	cleanupKept(t, dir, "agent-net/w_offline", repo)
	if strings.Contains(logs, "reason=merged") {
		t.Fatalf("log: %q", logs)
	}
}

// A repository with no remote keeps the old behavior: a locally merged branch
// is removed and a recent unmerged branch is kept.
func TestCleanupWorktreeNoRemoteUnchanged(t *testing.T) {
	t.Parallel()
	repo := newRepo(t, "project")
	state := t.TempDir()

	merged := cleanupAddWorktree(t, repo, state, "w_merged")
	cleanupCommit(t, merged, "change", "change")
	gitIn(t, repo, "merge", "-q", "--ff-only", "agent-net/w_merged")
	cleanupRun(t, state, cleanupCandidate("w_merged", repo, merged))
	cleanupGone(t, merged, "agent-net/w_merged", repo)

	unmerged := cleanupAddWorktree(t, repo, state, "w_unmerged")
	cleanupCommit(t, unmerged, "other", "other")
	cleanupRun(t, state, cleanupCandidate("w_unmerged", repo, unmerged))
	cleanupKept(t, unmerged, "agent-net/w_unmerged", repo)
}
