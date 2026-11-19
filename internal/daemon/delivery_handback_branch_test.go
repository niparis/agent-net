package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	id, problem := n.d.recordHandbackChange(context.Background(), w, nil)
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

			id, problem := n.d.recordHandbackChange(context.Background(), w, nil)
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

// A worker moves its worktree to a new branch made from main, which does not
// hold its first change's head, and hands back there. That work is a change of
// its own. The first change keeps its branch, head, revision and evidence. When
// the worker is back on its first branch, that hand-back is the first change's
// next revision.
func TestHandbackOnAnotherBranchIsAnotherChange(t *testing.T) {
	c, g, repo := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Add the dirty file", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
	handbackNotice(t, c, callerID, res.SessionID)
	headA := gitOutT(t, repo, "rev-parse", res.Branch)
	x := changeOnBranch(t, caller, res.Branch)
	reportStep(t, caller, protocol.StepReportArgs{Change: x.ID, StepRef: protocol.StepRef{Step: protocol.StepAccept},
		Result: protocol.StepResultPass, Commit: headA, Note: "read the diff"})
	eventually(t, "the worker ended", func() bool { return c.d.workerCount() == 0 })

	// The same worker, in the same worktree, starts a task on a new branch made
	// from main, and hands back there.
	gitIn(t, res.Worktree, "checkout", "-q", "-b", "chore/pin", "main")
	caller.mustCall("send", map[string]any{"to": res.SessionID, "body": "FAKE:edit FAKE:handback"})
	notices := handbackNotices(t, c, callerID, res.SessionID, 2)
	headB := gitOutT(t, repo, "rev-parse", "chore/pin")
	y := changeOnBranch(t, caller, "chore/pin")
	if y.ID == x.ID || y.HeadCommit != headB || y.Revision != 1 || !strings.Contains(notices[1].Body, "\nchange: "+y.ID) {
		t.Errorf("the branch hand-back: change %+v, notice:\n%s", y, notices[1].Body)
	}
	kept, err := g.gw.Store().GetChange(x.ID)
	if err != nil || kept.Change.Branch != res.Branch || kept.Change.HeadCommit != headA || kept.Change.Revision != 1 ||
		len(kept.Evidence) != 1 || kept.Evidence[0].Commit != headA {
		t.Errorf("the first change moved or lost its evidence: %+v evidence %+v %v", kept.Change, kept.Evidence, err)
	}

	// Back on the first branch, the same worker's hand-back is the first
	// change's next revision, and the branch change stays as it was.
	gitIn(t, res.Worktree, "checkout", "-q", res.Branch)
	caller.mustCall("send", map[string]any{"to": res.SessionID, "body": "FAKE:edit FAKE:handback"})
	notices = handbackNotices(t, c, callerID, res.SessionID, 3)
	headA2 := gitOutT(t, repo, "rev-parse", res.Branch)
	if headA2 == headA {
		t.Fatal("the return to the first branch made no commit")
	}
	if !strings.Contains(notices[2].Body, "\nchange: "+x.ID) {
		t.Errorf("the return does not name change %s:\n%s", x.ID, notices[2].Body)
	}
	back, err := g.gw.Store().GetChange(x.ID)
	if err != nil || back.Change.Revision != 2 || back.Change.HeadCommit != headA2 || back.Change.Branch != res.Branch {
		t.Errorf("the first change after the return: %+v %v", back.Change, err)
	}
	other, err := g.gw.Store().GetChange(y.ID)
	if err != nil || other.Change.Branch != "chore/pin" || other.Change.HeadCommit != headB || other.Change.Revision != 1 {
		t.Errorf("the branch change after the return: %+v %v", other.Change, err)
	}
}

// A new branch made from the change's own branch holds its head, so the change
// follows the work there as its next revision. A branch made from main does not,
// and its work is another change.
func TestHandbackOnANewBranchFollowsTheChangeOnlyFromItsHead(t *testing.T) {
	cases := []struct {
		name       string
		fromChange bool
	}{
		{"a branch made from the change's branch moves the change", true},
		{"a branch made from main is another change", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, g, repo := deliveryHandbackNode(t, nil)
			caller, callerID := deliveryCaller(t, c)
			res := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
				Title: "Add the dirty file", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree})
			handbackNotice(t, c, callerID, res.SessionID)
			x := changeOnBranch(t, caller, res.Branch)
			eventually(t, "the worker ended", func() bool { return c.d.workerCount() == 0 })

			from := "main"
			if tc.fromChange {
				from = res.Branch
			}
			gitIn(t, res.Worktree, "checkout", "-q", "-b", "chore/next", from)
			caller.mustCall("send", map[string]any{"to": res.SessionID, "body": "FAKE:edit FAKE:handback"})
			notices := handbackNotices(t, c, callerID, res.SessionID, 2)
			head := gitOutT(t, repo, "rev-parse", "chore/next")
			if tc.fromChange {
				moved, err := g.gw.Store().GetChange(x.ID)
				if err != nil || moved.Change.Branch != "chore/next" || moved.Change.HeadCommit != head || moved.Change.Revision != 2 ||
					!strings.Contains(notices[1].Body, "\nchange: "+x.ID) {
					t.Errorf("the change after its branch moved: %+v %v\n%s", moved.Change, err, notices[1].Body)
				}
				return
			}
			kept, err := g.gw.Store().GetChange(x.ID)
			y := changeOnBranch(t, caller, "chore/next")
			if err != nil || kept.Change.Branch != res.Branch || kept.Change.Revision != 1 || y.ID == x.ID || y.HeadCommit != head ||
				!strings.Contains(notices[1].Body, "\nchange: "+y.ID) {
				t.Errorf("the first change %+v %v, the branch change %+v\n%s", kept.Change, err, y, notices[1].Body)
			}
		})
	}
}

// A continued worker starts on a branch made from its old one, which holds the
// change's head, so the change follows it there. A continued worker that moves
// its work to a branch made from main does not hold that head, so its work is a
// change of its own.
func TestContinuedWorkerOnAnotherBranchIsAnotherChange(t *testing.T) {
	c, g, repo := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	// The first task commits its file. A continue sends the old task again with
	// the new one, so FAKE:dirty would leave an untracked file on the new branch.
	first := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Add the work file", Task: "FAKE:edit FAKE:handback", Isolation: protocol.IsolationWorktree})
	handbackNotice(t, c, callerID, first.SessionID)
	headA := gitOutT(t, repo, "rev-parse", first.Branch)
	x := changeOnBranch(t, caller, first.Branch)
	reportStep(t, caller, protocol.StepReportArgs{Change: x.ID, StepRef: protocol.StepRef{Step: protocol.StepAccept},
		Result: protocol.StepResultPass, Commit: headA, Note: "read the diff"})
	eventually(t, "the worker ended", func() bool { return c.d.workerCount() == 0 })

	var next protocol.WorkerStartResult
	if err := json.Unmarshal(caller.mustCall("worker_start", protocol.WorkerStartRequest{Continue: first.SessionID,
		Task: "FAKE:newbranch FAKE:edit FAKE:handback"}), &next); err != nil || next.SessionID == "" {
		t.Fatalf("continue: %+v %v", next, err)
	}
	n := handbackNotice(t, c, callerID, next.SessionID)
	headB := gitOutT(t, repo, "rev-parse", "chore/pin")
	y := changeOnBranch(t, caller, "chore/pin")
	if y.ID == x.ID || y.HeadCommit != headB || y.Revision != 1 || y.Uncommitted || !strings.Contains(n.Body, "\nchange: "+y.ID) {
		t.Errorf("the continued hand-back: change %+v\n%s", y, n.Body)
	}
	kept, err := g.gw.Store().GetChange(x.ID)
	if err != nil || kept.Change.Branch != first.Branch || kept.Change.HeadCommit != headA || kept.Change.Revision != 1 ||
		len(kept.Evidence) != 1 || kept.Evidence[0].Commit != headA {
		t.Errorf("the first change moved or lost its evidence: %+v evidence %+v %v", kept.Change, kept.Evidence, err)
	}
}

// changeOnBranch returns the change of the shop project on branch. A test has
// one open change per branch, so the branch names the change.
func changeOnBranch(t *testing.T, lc *localClient, branch string) protocol.Change {
	t.Helper()
	for _, item := range findChanges(t, lc, protocol.ChangesFindArgs{Project: "shop", State: protocol.ChangeListAny}).Items {
		if item.Change.Branch == branch {
			return item.Change
		}
	}
	t.Fatalf("no change on branch %s", branch)
	return protocol.Change{}
}

// handbackNotices waits until session has sent its caller want hand-back
// notices, and returns them oldest first.
func handbackNotices(t *testing.T, c *claudeNode, callerID, session string, want int) []protocol.Message {
	t.Helper()
	var got []protocol.Message
	eventually(t, "the hand-back notices of "+session, func() bool {
		got = got[:0]
		for _, m := range c.messagesOf(t, callerID) {
			if m.From == session && strings.Contains(m.Body, protocol.ReportMarker) {
				got = append(got, m)
			}
		}
		return len(got) >= want
	})
	slices.SortFunc(got, func(a, b protocol.Message) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return got
}
