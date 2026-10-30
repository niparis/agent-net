package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker stopped in the middle of a git merge is not committed: a commit
// would drop the merge parent. The worktree is left as it is.
func TestStoppedWorkerInAMergeIsLeftAsIs(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "merge main", Isolation: protocol.IsolationWorktree})
	if err := os.WriteFile(filepath.Join(repo, "main-only.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "main-only.txt")
	gitIn(t, repo, "commit", "-q", "-m", "main moves on")
	gitIn(t, res.Worktree, "-c", "user.name=t", "-c", "user.email=t@example.com", "merge", "--no-commit", "--no-ff", "main")
	before := gitOutT(t, repo, "rev-parse", res.Branch)
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "A git merge was in progress; the worktree was left as is.") {
		t.Errorf("notice lacks the merge line:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "unfinished changes were saved") {
		t.Errorf("notice says the changes were saved:\n%s", n.Body)
	}
	if after := gitOutT(t, repo, "rev-parse", res.Branch); after != before {
		t.Errorf("the branch moved from %s to %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "main-only.txt")); err != nil {
		t.Errorf("the worktree was not kept as it was: %v", err)
	}
	if gitOutT(t, res.Worktree, "rev-parse", "--verify", "MERGE_HEAD") == "" {
		t.Error("MERGE_HEAD is gone")
	}
}
