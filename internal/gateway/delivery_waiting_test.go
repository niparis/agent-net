package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func waitingRig(t *testing.T) (*deliveryRig, protocol.Change, protocol.StepRef) {
	t.Helper()
	r := newDeliveryRig(t)
	callerNeedsSharedPipeline(t, r)
	c := mergedChange(t, r, "feature/wait", true)
	ref := protocol.StepRef{Step: protocol.StepVerify, AfterMerge: true}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepRelease, AfterMerge: true}, Result: "pass", Commit: c.MergedCommit})
	if _, err := r.s.db.Exec("UPDATE delivery_step_tasks SET completed=1 WHERE step<>'verify'"); err != nil {
		t.Fatal(err)
	}
	r.g.dispatchStepTasks(context.Background())
	return r, c, ref
}
func reportWaiting(t *testing.T, r *deliveryRig, c protocol.Change, ref protocol.StepRef, until string) {
	t.Helper()
	q := protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: ref, Result: "waiting", Commit: c.MergedCommit, Note: "owner opens console"}
	if until != "" {
		raw, _ := json.Marshal(map[string]string{"until": until})
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.report(r.a, q); err != nil {
		t.Fatal(err)
	}
}
func waitingDispatch(r *deliveryRig) {
	r.g.dispatchStepTasks(context.Background())
	r.g.flushStepTasks(r.caller)
}
func TestWaitingVerifyReminder(t *testing.T) {
	r, c, ref := waitingRig(t)
	reportWaiting(t, r, c, ref, "")
	waitAt := waitingRecordedAt(t, r, c)
	n := len(taskBodies(t, r))
	r.clk.t = r.clk.t.Add(23 * time.Hour)
	waitingDispatch(r)
	if got := len(taskBodies(t, r)); got != n {
		t.Fatalf("during wait got %d notices, want %d", got, n)
	}
	r.clk.t = r.clk.t.Add(time.Hour)
	waitingDispatch(r)
	assertWaitingExpiredWithoutRefresh(t, r, c, ref, waitAt)
	bodies := taskBodies(t, r)
	if len(bodies) != n+1 || !strings.Contains(bodies[len(bodies)-1], "was waiting on: owner opens console") {
		t.Fatalf("resumed notices: %v", bodies)
	}
	waitingDispatch(r)
	if len(taskBodies(t, r)) != n+1 {
		t.Fatal("burst after wait")
	}
}
func TestWaitingNewEvidence(t *testing.T) {
	r, c, ref := waitingRig(t)
	reportWaiting(t, r, c, ref, "")
	r.clk.t = r.clk.t.Add(time.Hour)
	if _, err := r.s.db.Exec(`INSERT INTO delivery_evidence(change_id,revision,step,for_step,after_merge,result,commit_hash,fields,by_session,note,at) SELECT change_id,revision,step,for_step,after_merge,result,commit_hash,fields,by_session,note,? FROM delivery_evidence WHERE change_id=? LIMIT 1`, r.clk.t.Unix(), c.ID); err != nil {
		t.Fatal(err)
	}
	n := len(taskBodies(t, r))
	waitingDispatch(r)
	if len(taskBodies(t, r)) != n+1 {
		t.Fatal("new evidence did not resume reminder")
	}
}
func TestWaitingUntil(t *testing.T) {
	r, c, ref := waitingRig(t)
	start := r.clk.t
	reportWaiting(t, r, c, ref, start.Add(48*time.Hour).Format(time.RFC3339))
	waitAt := waitingRecordedAt(t, r, c)
	n := len(taskBodies(t, r))
	for _, h := range []int{47, 48, 71} {
		r.clk.t = start.Add(time.Duration(h) * time.Hour)
		waitingDispatch(r)
		if len(taskBodies(t, r)) != n {
			t.Fatalf("reminded at %dh", h)
		}
	}
	r.clk.t = start.Add(72 * time.Hour)
	waitingDispatch(r)
	assertWaitingExpiredWithoutRefresh(t, r, c, ref, waitAt)
	if len(taskBodies(t, r)) != n+1 {
		t.Fatal("wait did not expire")
	}
}
func TestWaitingNoStuck(t *testing.T) {
	r, c, ref := waitingRig(t)
	reportWaiting(t, r, c, ref, "")
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.checkStuckStepTasks(context.Background(), 12*time.Hour)
	var n int
	if err := r.s.db.QueryRow("SELECT count(*) FROM messages WHERE body LIKE '%nobody claimed%'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("stuck notices=%d err=%v", n, err)
	}
}
func TestWaitingEligibility(t *testing.T) {
	r, c, ref := waitingRig(t)
	r.moveToAgent(r.sonnet, "peer")
	_, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: c.ID, StepRef: ref, Result: "waiting", Commit: c.MergedCommit, Note: "event"})
	if err == nil {
		t.Fatal("ineligible session accepted")
	}
	if !strings.Contains(err.Error(), "owned") {
		t.Fatalf("not an eligibility refusal: %v", err)
	}
}
func TestWaitingDoesNotClaimOrRecordEvidence(t *testing.T) {
	r, c, ref := waitingRig(t)
	var before, after int
	r.s.db.QueryRow("SELECT count(*) FROM delivery_evidence").Scan(&before)
	reportWaiting(t, r, c, ref, "")
	r.s.db.QueryRow("SELECT count(*) FROM delivery_evidence").Scan(&after)
	var claimed string
	var completed int
	err := r.s.db.QueryRow("SELECT claimed_by,completed FROM delivery_step_tasks WHERE change_id=? AND step='verify'", c.ID).Scan(&claimed, &completed)
	if err != nil || claimed != "" || completed != 0 || after != before {
		t.Fatalf("claim=%q complete=%d evidence=%d/%d err=%v", claimed, completed, before, after, err)
	}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: ref, Commit: c.MergedCommit}); err != nil {
		t.Fatal(err)
	}
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: ref, Commit: c.MergedCommit, Result: "pass"})
}
func TestWaitingChangesFind(t *testing.T) {
	r, c, ref := waitingRig(t)
	reportWaiting(t, r, c, ref, "")
	listed := r.list(protocol.ChangeListRequest{State: protocol.ChangeListAny})
	if len(listed.Changes) != 1 {
		t.Fatalf("changes: %+v", listed)
	}
	v := listed.Changes[0]
	for _, st := range v.Steps {
		if st.StepRef == ref && st.State == "waiting" && strings.Contains(st.Note, "waiting on owner opens console") {
			return
		}
	}
	t.Fatalf("wait missing: %+v", v.Steps)
}

func TestWaitingOrdinaryReminderCadence(t *testing.T) {
	r := setupMergerRig(t)
	c := r.submit(r.a, r.workerSubmit("feature/wait-cadence", commitA)).Change.Change
	r.g.dispatchStepTasks(context.Background())
	n := len(taskBodies(t, r))
	_, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: "waiting", Commit: commitA, Note: "event"})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(time.Hour)
	tx, err := r.s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := insertEvidence(tx, protocol.Evidence{ChangeID: c.ID, Revision: 1, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: "pass", Commit: commitA, By: r.caller, At: r.clk.t.Unix()}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	waitingDispatch(r)
	if len(taskBodies(t, r)) != n {
		t.Fatal("ordinary reminder arrived before its existing 2h cadence")
	}
	r.clk.t = r.clk.t.Add(time.Hour + time.Minute)
	waitingDispatch(r)
	if len(taskBodies(t, r)) != n+1 {
		t.Fatal("ordinary reminder did not resume on cadence")
	}
}

func waitingRecordedAt(t *testing.T, r *deliveryRig, c protocol.Change) int64 {
	t.Helper()
	var at int64
	if err := r.s.db.QueryRow(`SELECT waiting_at FROM delivery_step_tasks WHERE change_id=? AND step='verify' AND after_merge=1`, c.ID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func assertWaitingExpiredWithoutRefresh(t *testing.T, r *deliveryRig, c protocol.Change, ref protocol.StepRef, at int64) {
	t.Helper()
	if got := waitingRecordedAt(t, r, c); got != at {
		t.Fatalf("batch refreshed waiting_at: %d want %d", got, at)
	}
	active, _, err := r.s.stepWaiting(nodeTaskTarget{id: c.ID, revision: c.Revision, ref: ref, session: r.caller})
	if err != nil || active {
		t.Fatalf("wait remains active after expiry: active=%v err=%v", active, err)
	}
}
