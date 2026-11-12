package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A fix-round worker started on the branch of an open change hands its work
// back to that change, as its next revision, not as a second change.
func TestHandbackOnAnOpenChangeBranchIsTheNextRevision(t *testing.T) {
	c, _, repo := deliveryHandbackNode(t, nil)
	caller, callerID := deliveryCaller(t, c)
	first := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Add the dirty file", Task: "FAKE:dirty FAKE:handback", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	handbackNotice(t, c, callerID, first.SessionID)
	before := findChanges(t, caller, protocol.ChangesFindArgs{Project: "shop"})
	if len(before.Items) != 1 {
		t.Fatalf("the first worker recorded %d changes: %+v", len(before.Items), before.Items)
	}
	ch := before.Items[0].Change
	if base := gitOutT(t, repo, "rev-parse", first.Branch); ch.HeadCommit != base {
		t.Fatalf("the change head %s is not the branch head %s", ch.HeadCommit, base)
	}

	fix := startDeliveryWorker(t, c, caller, callerID, protocol.WorkerStartRequest{Model: "sonnet", Project: "shop", Harness: "claude",
		Title: "Fix the test", Task: "FAKE:edit FAKE:handback", Isolation: protocol.IsolationWorktree, Base: first.Branch, AutoContinue: autoContinueOff()})
	n := handbackNotice(t, c, callerID, fix.SessionID)
	head := gitOutT(t, repo, "rev-parse", fix.Branch)

	all := findChanges(t, caller, protocol.ChangesFindArgs{Project: "shop", State: protocol.ChangeListAny})
	if len(all.Items) != 1 {
		t.Fatalf("the fix round made another change instead of a revision: %+v", all.Items)
	}
	got := all.Items[0].Change
	if got.ID != ch.ID || got.Revision != 2 || got.Branch != fix.Branch || got.HeadCommit != head {
		t.Errorf("the fix round recorded %+v, want change %s revision 2 on %s at %s", got, ch.ID, fix.Branch, head)
	}
	if !strings.Contains(n.Body, "\nchange: "+ch.ID) {
		t.Errorf("the notice does not name change %s:\n%s", ch.ID, n.Body)
	}
}

// A worker that rebased onto a newer main reports only its own commits: the
// diff stat and the signals come from the merge-base of its head with main,
// not from the start commit it rebased away from.
func TestHandbackDiffIgnoresCommitsFromANewerMain(t *testing.T) {
	t.Parallel()
	n, f := startDeliveryNode(t)
	f.answer(protocol.FrameDeliveryNeedsPush, func(json.RawMessage) (any, error) {
		return protocol.DeliveryNeedsPushResult{}, nil
	})
	repo, wt := deliveryWorktree(t)
	start := gitHead(t, wt, "HEAD")

	// Other people's work lands on main: a large change and a test that sleeps,
	// the shapes the quality signals call out.
	big := strings.Repeat("a line of another change\n", 700)
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	other := "package demo\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestOther(t *testing.T) {\n\ttime.Sleep(10 * time.Millisecond)\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "other_test.go"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "other.txt", "other_test.go")
	gitIn(t, repo, "commit", "-q", "-m", "other people's work")

	// The worker rebases onto the newer main, then makes its own small fix.
	gitIn(t, wt, "rebase", "-q", "main")
	fix := "package demo\n\nimport \"testing\"\n\nfunc TestFix(t *testing.T) {\n\tif 1 != 1 {\n\t\tt.Fatal(\"one\")\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(wt, "fix_test.go"), []byte(fix), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "fix_test.go")
	gitIn(t, wt, "commit", "-q", "-m", "the fix")

	w := &worker{id: "w_rebased", worktree: wt, branch: "feat/x", base: start, owner: "s_owner", sessionID: "s_worker", project: "demo", difficultyUsed: "normal"}
	id, problem := n.d.recordHandbackChange(context.Background(), w, nil)
	if id == "" || problem != "" {
		t.Fatalf("id=%q problem=%q", id, problem)
	}
	frames := f.frames(protocol.FrameChangeSubmit)
	var q protocol.ChangeSubmitRequest
	if len(frames) != 1 || json.Unmarshal(frames[0], &q) != nil {
		t.Fatalf("change.submit frames: %s", frames)
	}
	if strings.Contains(q.DiffStat, "other.txt") || !strings.Contains(q.DiffStat, "fix_test.go") {
		t.Errorf("diff stat counts the newer main:\n%s", q.DiffStat)
	}
	for _, s := range q.Signals {
		if s.Code == protocol.SignalTestTiming || s.Code == protocol.SignalOutOfProportion {
			t.Errorf("signal %s comes from another change: %s", s.Code, s.Evidence)
		}
	}
	// The notice's "changes (git diff --stat)" reads the same base.
	if stat, _ := n.d.worktreeFacts(w); strings.Contains(stat, "other.txt") || !strings.Contains(stat, "fix_test.go") {
		t.Errorf("the notice's diff stat counts the newer main:\n%s", stat)
	}
}
