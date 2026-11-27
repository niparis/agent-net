package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestCodexLinkedWorktreeWritableRoots(t *testing.T) {
	repo := newRepo(t, "linked")
	linked := filepath.Join(t.TempDir(), "checkout")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "-b", "linked-test", linked)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree: %v: %s", err, out)
	}
	got := workerGitRoots(context.Background(), linked)
	common := filepath.Join(repo, ".git")
	want := []string{filepath.Join(common, "worktrees", "checkout"), filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
}

// A worker gets the whole git folder of its repository, so commit, switch -c,
// push -u and worktree add work in a linked worktree (decision 0197).
func TestWorkerGitDirsIsTheWholeGitFolder(t *testing.T) {
	repo := newRepo(t, "linked")
	linked := filepath.Join(t.TempDir(), "checkout")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "linked-test", linked)
	got := workerGitDirs(context.Background(), linked)
	if want := []string{filepath.Join(repo, ".git")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
