package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker that moved to a new branch and committed there hands back that
// branch and its head, not the start branch the daemon created for it.
func TestHandbackRecordsTheBranchTheWorkerMovedTo(t *testing.T) {
	t.Parallel()
	n, f := startDeliveryNode(t)
	f.answer(protocol.FrameDeliveryNeedsPush, func(json.RawMessage) (any, error) {
		return protocol.DeliveryNeedsPushResult{}, nil
	})
	repo, wt := deliveryWorktree(t)
	gitIn(t, wt, "checkout", "-q", "-b", "chore/pin")
	if err := os.WriteFile(filepath.Join(wt, "pin.txt"), []byte("pin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "pin.txt")
	gitIn(t, wt, "commit", "-q", "-m", "pin")
	head := gitHead(t, wt, "HEAD")
	w := &worker{id: "w_moved", worktree: wt, branch: "feat/x", base: gitHead(t, repo, "main"), owner: "s_owner", sessionID: "s_worker", project: "demo", difficultyUsed: "normal"}

	id, problem := n.d.recordHandbackChange(context.Background(), w)
	if id == "" || problem != "" {
		t.Fatalf("id=%q problem=%q", id, problem)
	}
	frames := f.frames(protocol.FrameChangeSubmit)
	var q protocol.ChangeSubmitRequest
	if len(frames) != 1 || json.Unmarshal(frames[0], &q) != nil {
		t.Fatalf("change.submit frames: %s", frames)
	}
	if q.Branch != "chore/pin" || q.HeadCommit != head {
		t.Fatalf("submit branch=%q head=%q, want chore/pin at %s", q.Branch, q.HeadCommit, head)
	}
}

// A worker that stays on its start branch records that branch, as before.
// A new branch with no commits beyond the base, or a detached HEAD, records
// the start branch too.
func TestHandbackKeepsTheStartBranchWhenTheWorkerDidNotMove(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		move func(t *testing.T, wt string)
	}{
		{"stays on the start branch", func(t *testing.T, wt string) {}},
		{"new branch without commits", func(t *testing.T, wt string) {
			gitIn(t, wt, "checkout", "-q", "-b", "empty", "main")
		}},
		{"detached head", func(t *testing.T, wt string) {
			gitIn(t, wt, "checkout", "-q", "--detach")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, f := startDeliveryNode(t)
			f.answer(protocol.FrameDeliveryNeedsPush, func(json.RawMessage) (any, error) {
				return protocol.DeliveryNeedsPushResult{}, nil
			})
			repo, wt := deliveryWorktree(t)
			tc.move(t, wt)
			head := gitHead(t, wt, "feat/x")
			w := &worker{id: "w_still", worktree: wt, branch: "feat/x", base: gitHead(t, repo, "main"), owner: "s_owner", sessionID: "s_worker", project: "demo", difficultyUsed: "normal"}

			id, problem := n.d.recordHandbackChange(context.Background(), w)
			if id == "" || problem != "" {
				t.Fatalf("id=%q problem=%q", id, problem)
			}
			frames := f.frames(protocol.FrameChangeSubmit)
			var q protocol.ChangeSubmitRequest
			if len(frames) != 1 || json.Unmarshal(frames[0], &q) != nil {
				t.Fatalf("change.submit frames: %s", frames)
			}
			if q.Branch != "feat/x" || q.HeadCommit != head {
				t.Fatalf("submit branch=%q head=%q, want feat/x at %s", q.Branch, q.HeadCommit, head)
			}
		})
	}
}

// The notice names the recorded branch only when it is not the start branch.
func TestDeliveredBranchIsEmptyForTheStartBranch(t *testing.T) {
	t.Parallel()
	n, _ := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	w := &worker{id: "w_notice", worktree: wt, branch: "feat/x", base: gitHead(t, repo, "main")}
	if got := n.d.deliveredBranch(context.Background(), w); got != "" {
		t.Fatalf("staying on the start branch: deliveredBranch = %q, want empty", got)
	}
	gitIn(t, wt, "checkout", "-q", "-b", "chore/pin")
	if got := n.d.deliveredBranch(context.Background(), w); got != "chore/pin" {
		t.Fatalf("moved to chore/pin: deliveredBranch = %q", got)
	}
}
