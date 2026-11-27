package daemon

import (
	"context"
	"os"
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

// A worker writes the git folder of its repository, so commit, switch -c and
// worktree add work in a linked worktree, but not what git runs code from
// outside the sandbox: the config, the hooks (and the core.hooksPath folder),
// info and the other linked worktrees' folders (decision 0197).
func TestWorkerGitFenceKeepsConfigAndHooksReadOnly(t *testing.T) {
	repo := newRepo(t, "linked")
	common := filepath.Join(repo, ".git")
	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "config", "core.hooksPath", hooks)
	base := t.TempDir()
	gitIn(t, repo, "worktree", "add", "-q", "-b", "mine", filepath.Join(base, "mine"))
	gitIn(t, repo, "worktree", "add", "-q", "-b", "other", filepath.Join(base, "other"))
	f := workerGitFence(context.Background(), filepath.Join(base, "mine"))
	if want := []string{common}; !reflect.DeepEqual(f.Write, want) {
		t.Fatalf("writable %v, want %v", f.Write, want)
	}
	for _, want := range []string{filepath.Join(common, "config"), filepath.Join(common, "hooks"), filepath.Join(common, "info"),
		hooks, filepath.Join(common, "worktrees", "other")} {
		if !hasString(f.ReadOnly, want) {
			t.Errorf("%s is writable: read-only %v", want, f.ReadOnly)
		}
	}
	if own := filepath.Join(common, "worktrees", "mine"); hasString(f.ReadOnly, own) {
		t.Errorf("the worktree's own git folder %s is read-only, so it cannot commit", own)
	}
}

// A core.hooksPath above the worktree is not made read-only: it would cover
// the worktree, and the hooks there are out of the worker's reach anyway.
func TestWorkerFenceKeepsAHooksPathAboveTheWorktreeWritable(t *testing.T) {
	repo := newRepo(t, "linked")
	base := t.TempDir()
	gitIn(t, repo, "config", "core.hooksPath", base)
	gitIn(t, repo, "worktree", "add", "-q", "-b", "mine", filepath.Join(base, "mine"))
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	f := d.workerFence(context.Background(), &worker{id: "w_fence", path: filepath.Join(base, "mine")})
	if hasString(f.ReadOnly, base) {
		t.Fatalf("the hooks folder %s holds the worktree and is read-only: %v", base, f.ReadOnly)
	}
	if want := filepath.Join(repo, ".git", "config"); !hasString(f.ReadOnly, want) {
		t.Fatalf("%s is writable: read-only %v", want, f.ReadOnly)
	}
}
