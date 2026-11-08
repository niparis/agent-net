package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCoalesceSingleIsolatedTask(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("tester", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "test", Node: "tester"}, "owner"); err != nil {
		t.Fatal(err)
	}

	// Single isolated change submitted.
	q := r.workerSubmit("feature/isolated", commitA)
	q.DiffStat = "main.go | 2 +-\n1 file changed, 1 insertion(+), 1 deletion(-)"
	res := r.submit(r.a, q)
	cid := res.Change.Change.ID

	r.g.dispatchStepTasks(context.Background())

	var n int
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil || n != 1 {
		t.Fatalf("message count=%d want=1 err=%v", n, err)
	}

	var body string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE session_id=?)`, r.caller).Scan(&body); err != nil {
		t.Fatal(err)
	}

	// Single isolated task keeps today's full format.
	if !strings.HasPrefix(body, "[agent-net step task]\n") {
		t.Fatalf("expected single task prefix [agent-net step task], got: %s", body)
	}
	if !strings.Contains(body, cid) || !strings.Contains(body, "diff stat:") || !strings.Contains(body, "evidence:") {
		t.Fatalf("single task missing details: %s", body)
	}
}

func TestCoalesceMixedBurstTwoRecipients(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}

	// Give reviewer a session on node B
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "rev", Repo: protocol.RepoInfo{Project: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}

	// Submit changes for caller
	for i := 0; i < 3; i++ {
		r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/burst-caller-%d", i), commitA))
	}

	// Reviewer also gets tasks directly queued
	for i := 0; i < 3; i++ {
		changeID := fmt.Sprintf("ch_burst_rev_%d", i)
		branch := fmt.Sprintf("burst-rev-%d", i)
		if _, err := r.s.db.Exec(`INSERT INTO delivery_changes(id,project,machine,branch,target_branch,base_commit,head_commit,revision,origin,caller,title,difficulty,state,created_at,updated_at)
			VALUES(?,'demo',?,?,'main',?,?,1,'{"kind":"person"}',?,'burst',0,'open',1,1)`,
			changeID, r.a, branch, commitBase, commitA, r.caller); err != nil {
			t.Fatal(err)
		}
		if _, err := r.s.db.Exec(`INSERT INTO delivery_step_tasks(change_id,revision,step,for_step,after_merge,owner_node,created_at)
			VALUES(?,1,'review','',0,'reviewer',1)`, changeID); err != nil {
			t.Fatal(err)
		}
	}

	r.g.dispatchStepTasks(context.Background())

	checkRecipient := func(sessionID string, label string) {
		t.Helper()
		var n int
		if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, sessionID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s message count=%d want=1 err=%v", label, n, err)
		}
		var body string
		if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE session_id=?)`, sessionID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(body, "[agent-net step tasks]\n") {
			t.Fatalf("%s expected batch header [agent-net step tasks], got: %s", label, body)
		}
	}

	checkRecipient(r.caller, "caller")
	checkRecipient(sid, "reviewer")
}

func TestCoalesceBatchedTaskClaimAndReport(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}

	// Create two tasks so they form a batch
	var cids []string
	for i := 0; i < 2; i++ {
		cids = append(cids, r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/claim-%d", i), commitA)).Change.Change.ID)
	}

	r.g.dispatchStepTasks(context.Background())

	var n int
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil || n != 1 {
		t.Fatalf("expected 1 batch message, got %d", n)
	}

	// Claim task 0
	claimRes, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{
		SessionID: r.caller,
		ChangeID:  cids[0],
		StepRef:   stepAccept,
		Commit:    commitA,
	})
	if err != nil || !claimRes.Claimed {
		t.Fatalf("ClaimStep failed: res=%+v err=%v", claimRes, err)
	}

	// Report task 0
	repRes, err := r.report(r.a, protocol.StepReportRequest{
		SessionID: r.caller,
		ChangeID:  cids[0],
		StepRef:   stepAccept,
		Result:    protocol.StepResultPass,
		Commit:    commitA,
	})
	if err != nil {
		t.Fatalf("ReportStep failed: %v", err)
	}
	if repRes.Evidence.Result != protocol.StepResultPass {
		t.Fatalf("unexpected evidence result: %s", repRes.Evidence.Result)
	}
}

func TestCoalesceWakeOnDemandOnceForRetiredOwner(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}

	r.g.conns = map[string]*nodeConn{}
	wakes := make(chan string, 10)
	fakeOwnerNode(t, r.g, r.a, func(h protocol.Header, raw []byte) error {
		if h.Type == protocol.FrameSessionWake {
			var p struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(raw, &p); err == nil {
				wakes <- p.SessionID
			}
		}
		return nil
	})

	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}

	// Submit multiple changes while caller is offline
	for i := 0; i < 3; i++ {
		r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/wake-test-%d", i), commitA))
	}

	select {
	case id := <-wakes:
		if id != r.caller {
			t.Fatalf("woke %s, want %s", id, r.caller)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("offline caller was not woken")
	}

	// Make sure no extra wakes were sent
	select {
	case id := <-wakes:
		t.Fatalf("extra wake sent for %s", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCoalesce25ChangesReleaseThenVerifyGiveAtMostTwoMessages(t *testing.T) {
	r := newDeliveryRig(t)
	r.g.stepTaskQuietWindow = 50 * time.Millisecond
	r.g.stepTaskBatchCap = 25

	const numChanges = 25
	var changeIDs []string
	yes := true

	for i := 0; i < numChanges; i++ {
		c := r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/bulk-%d", i), commitA)).Change.Change
		changeIDs = append(changeIDs, c.ID)

		// accept
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: "pass", Commit: commitA,
		})
		// test before merge
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Result: "pass", Commit: commitA, Fields: map[string]string{"suite": "full"},
		})
		// merge
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "merge"}, Result: "pass", Commit: commitA, Fields: map[string]string{"merged_commit": commitMerged}, MergeContainsHead: &yes,
		})
		// CI (test after merge)
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test", AfterMerge: true}, Result: "pass", Commit: commitMerged, Fields: map[string]string{"suite": "full"},
		})
	}

	// At this point, release is ready for all 25 changes.
	// Wait for the quiet window to flush the batch.
	time.Sleep(100 * time.Millisecond)

	var msgsBeforeVerify int
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&msgsBeforeVerify); err != nil {
		t.Fatal(err)
	}

	// Now report release for all 25 changes, which makes verify ready for each change.
	for _, cid := range changeIDs {
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: cid, StepRef: protocol.StepRef{Step: "release", AfterMerge: true}, Result: "pass", Commit: commitMerged,
		})
	}

	// Wait for the quiet window to flush the verify batch.
	time.Sleep(100 * time.Millisecond)

	var totalMessages int
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&totalMessages); err != nil {
		t.Fatal(err)
	}

	if totalMessages > 2 {
		t.Fatalf("expected at most 2 messages for 25 changes passing release then verify, got %d (before verify: %d)", totalMessages, msgsBeforeVerify)
	}
}

func TestCoalesce25CallerVerifyTasksWithCallerDeliveryEdgeGiveOneBatch(t *testing.T) {
	r := newDeliveryRig(t)
	// The first phase never lets a timer fire: it is flushed by hand below.
	// A timer that came late under host load could otherwise send release
	// tasks while the verify tasks join the same batch, and the batch cap
	// would cut the verify tasks in two.
	r.g.stepTaskQuietWindow = time.Hour
	r.g.stepTaskBatchCap = 25

	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT OR IGNORE INTO graph_edges(source,target,kind,mode,rule)
		VALUES('/delivery/caller','demo','tests','',''),('/delivery/caller','demo','merges','','')`); err != nil {
		t.Fatal(err)
	}

	const numChanges = 25
	var changeIDs []string
	yes := true

	for i := 0; i < numChanges; i++ {
		c := r.submit(r.a, r.workerSubmit(fmt.Sprintf("feature/caller-edge-%d", i), commitA)).Change.Change
		changeIDs = append(changeIDs, c.ID)

		// accept
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: "pass", Commit: commitA,
		})
		// test before merge
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test"}, Result: "pass", Commit: commitA, Fields: map[string]string{"suite": "full"},
		})
		// merge
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "merge"}, Result: "pass", Commit: commitA, Fields: map[string]string{"merged_commit": commitMerged}, MergeContainsHead: &yes,
		})
		// CI (test after merge)
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: "test", AfterMerge: true}, Result: "pass", Commit: commitMerged, Fields: map[string]string{"suite": "full"},
		})
	}

	// Send the prior batches (e.g. release tasks) now.
	r.g.flushStepTaskBatches()
	// The verify burst gets a real quiet window. It is long next to the time
	// 25 reports take, so a slow scheduler does not open a gap inside the burst.
	r.g.stepTaskQuietWindow = 1500 * time.Millisecond

	// Now report release for all 25 changes, making verify ready for all of them.
	for _, cid := range changeIDs {
		r.mustReport(r.a, protocol.StepReportRequest{
			SessionID: r.caller, ChangeID: cid, StepRef: protocol.StepRef{Step: "release", AfterMerge: true}, Result: "pass", Commit: commitMerged,
		})
	}

	// Wait for the quiet window to flush the verify batch.
	verifyCount := func() int {
		var n int
		if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=? AND step='verify'`, r.caller).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	deadline := time.Now().Add(20 * time.Second)
	for verifyCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the quiet window did not send the verify batch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	verifyMessages := verifyCount()

	// All 25 tasks must be in the first message: a batch cut in two would
	// show here at once, before its second half is sent.
	var covered int
	if err := r.s.db.QueryRow(`SELECT count(DISTINCT change_id) FROM delivery_step_messages WHERE session_id=? AND step='verify'`, r.caller).Scan(&covered); err != nil {
		t.Fatal(err)
	}
	if verifyMessages != 1 || covered != numChanges {
		t.Fatalf("expected 1 batch message for 25 caller verify tasks with caller delivery edge, got %d messages covering %d changes", verifyMessages, covered)
	}

	var body string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE id IN (SELECT message_id FROM delivery_step_messages WHERE session_id=? AND step='verify')`, r.caller).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "[agent-net step tasks]\n") {
		t.Fatalf("expected batch prefix [agent-net step tasks], got %s", body)
	}
}

// A quiet-window timer that fired just before a new task re-armed the batch
// still runs its callback. That stale callback must not send the batch: the
// task that just joined has not had its quiet window, and sending would cut
// one burst into two notices.
func TestCoalesceStaleQuietTimerDoesNotCutTheBurst(t *testing.T) {
	r := newDeliveryRig(t)
	r.g.stepTaskQuietWindow = time.Hour // no real timer fires in this test
	r.g.stepTaskBatchCap = 25
	defer r.g.flushStepTaskBatches()
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "review", Node: "reviewer"}, "owner"); err != nil {
		t.Fatal(err)
	}

	// The first notice is sent at once, so the session counts as recent and
	// the next tasks wait in a batch.
	ctx := context.Background()
	r.submit(r.a, r.workerSubmit("feature/stale-0", commitA))
	r.g.dispatchStepTasks(ctx)
	r.submit(r.a, r.workerSubmit("feature/stale-1", commitA))
	r.g.dispatchStepTasks(ctx)
	armed := func() (gen uint64, waiting int) {
		r.g.stepBatchesMu.Lock()
		defer r.g.stepBatchesMu.Unlock()
		if b := r.g.stepBatches[r.caller]; b != nil {
			return b.timerGen, len(b.targets)
		}
		return 0, 0
	}
	stale, waiting := armed()
	if stale == 0 || waiting == 0 {
		t.Fatalf("after two submits: timer gen %d, %d waiting; want an armed batch", stale, waiting)
	}
	r.submit(r.a, r.workerSubmit("feature/stale-2", commitA))
	r.g.dispatchStepTasks(ctx)
	current, grown := armed()
	if current == stale || grown <= waiting {
		t.Fatalf("after three submits: timer gen %d (was %d), %d waiting (was %d); want a re-armed, larger batch", current, stale, grown, waiting)
	}
	count := func() int {
		var n int
		if err := r.s.db.QueryRow(`SELECT count(DISTINCT message_id) FROM delivery_step_messages WHERE session_id=?`, r.caller).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()

	r.g.flushStepTasksArmed(r.caller, stale) // the timer that was re-armed
	if _, left := armed(); left != grown || count() != before {
		t.Fatalf("a stale timer sent the batch: %d waiting (was %d), %d messages (was %d)", left, grown, count(), before)
	}
	r.g.flushStepTasksArmed(r.caller, current) // the timer that is armed
	if _, left := armed(); left != 0 || count() != before+1 {
		t.Fatalf("the armed timer did not send one batch: %d waiting, %d messages (was %d)", left, count(), before)
	}
}
