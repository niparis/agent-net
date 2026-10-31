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
	got := codexWritableRoots(context.Background(), &worker{path: linked})
	common := filepath.Join(repo, ".git")
	want := []string{filepath.Join(common, "worktrees", "checkout"), filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
}
