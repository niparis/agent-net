package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// slowGitOnWorktreeList puts a git on PATH that waits while the hold file
// exists when it runs "worktree list", and runs the real git otherwise. It
// returns the file that appears when a "worktree list" has started and the
// hold file to remove to let it go. Tests that call it must not run in parallel.
func slowGitOnWorktreeList(t *testing.T) (started, hold string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	started, hold = filepath.Join(dir, "started"), filepath.Join(dir, "hold")
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*"worktree list"*)
  touch %q
  while [ -e %q ]; do sleep 0.02; done;;
esac
exec %q "$@"
`, started, hold, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return started, hold
}

// A cleanup runs its git commands outside the locks that worker resumes,
// starts and the operator view take. On 2026-11-12 a resume waited for the
// resume lock behind cleanup passes over 75 worktrees and ran past the
// gateway's launch wait.
func TestCleanupWorktreeGitRunsOutsideTheWorkerLocks(t *testing.T) {
	project := newRepo(t, "project")
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "worktrees", "w_slowgit")
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, project, "worktree", "add", "-q", "-b", "agent-net/w_slowgit", dir)
	started, hold := slowGitOnWorktreeList(t)
	d := &Daemon{cfg: Config{StateDir: stateDir}, workers: map[string]*worker{}}
	candidate := cleanupWorktree{ID: "w_slowgit", Project: project, Path: dir, Branch: "agent-net/w_slowgit", EndedAt: time.Now().Add(-25 * time.Hour)}
	done := make(chan error, 1)
	go func() { done <- d.cleanupWorktree(context.Background(), candidate, time.Now()) }()
	defer func() {
		os.Remove(hold)
		<-done
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not run its git command")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for name, mu := range map[string]interface {
		TryLock() bool
		Unlock()
	}{"resume": &d.resumeMu, "queue": &d.qmu, "worker": &d.wmu} {
		if !mu.TryLock() {
			t.Fatalf("the %s lock is held while cleanup runs git", name)
		}
		mu.Unlock()
	}
}
