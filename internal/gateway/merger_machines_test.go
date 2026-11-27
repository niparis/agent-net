package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A project has one merger, and its branches reach origin from any machine.
// These tests put the merger (the rig's DeepSeek session) on machine b and
// make every change on machine a.

// mergerOnB registers the DeepSeek session on machine b as the project's
// merger and returns a change made on machine a, accepted by its caller.
func mergerOnB(t *testing.T, r *deliveryRig) protocol.Change {
	t.Helper()
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.b, SessionID: r.deepseek, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	c := r.submit(r.a, r.workerSubmit("feature/from-a", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	return c
}

// testNoticeTargets lists the sessions sent the change's pre-merge test task.
func testNoticeTargets(t *testing.T, r *deliveryRig, id string) []string {
	t.Helper()
	rows, err := r.s.db.Query(`SELECT session_id FROM `+stepNoticesSQL+` WHERE change_id=? AND step='test' AND after_merge=0`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	return got
}

func TestMergerOwnsChangesFromOtherMachines(t *testing.T) {
	r := newDeliveryRig(t)
	c := mergerOnB(t, r)
	for _, st := range r.get(c.ID).Steps {
		if st.Step == protocol.StepTest && strings.Contains(st.Note, "offline") {
			t.Fatalf("online merger on another machine shown offline: %+v", st)
		}
	}
	r.g.dispatchStepTasks(context.Background())
	if got := testNoticeTargets(t, r, c.ID); len(got) != 1 || got[0] != r.deepseek {
		t.Fatalf("test task sent to %v, want the merger %s", got, r.deepseek)
	}
	claim := protocol.StepClaimRequest{SessionID: r.deepseek, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepTest}, Commit: commitA}
	if _, err := deliveryCall[protocol.StepClaimResult](r, r.b, protocol.FrameStepClaim, claim); err != nil {
		t.Fatalf("merger on another machine cannot claim: %v", err)
	}
	r.mustReport(r.b, r.testReport(r.deepseek, c.ID, protocol.StepResultPass, map[string]string{"suite": "changed"}))
}

func TestMergerReplacedMovesOtherMachineTasks(t *testing.T) {
	r := newDeliveryRig(t)
	c := mergerOnB(t, r)
	claim := protocol.StepClaimRequest{SessionID: r.deepseek, ChangeID: c.ID, StepRef: protocol.StepRef{Step: protocol.StepTest}, Commit: commitA}
	if _, err := r.s.ClaimStep(r.b, claim); err != nil {
		t.Fatal(err)
	}
	next, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "replacement", Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.b, SessionID: next}, "owner"); err != nil {
		t.Fatal(err)
	}
	var claimed string
	if err := r.s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND step='test' AND after_merge=0`, c.ID).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed != "" {
		t.Fatalf("replaced merger %s still holds the claim", claimed)
	}
	r.g.dispatchStepTasks(context.Background())
	if got := testNoticeTargets(t, r, c.ID); len(got) != 1 || got[0] != next {
		t.Fatalf("test task sent to %v, want the new merger %s", got, next)
	}
}

func TestMergerStartMovesOtherMachineCallerTask(t *testing.T) {
	r := newDeliveryRig(t)
	c := r.submit(r.a, r.workerSubmit("feature/before-merger", commitA)).Change.Change
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: c.ID, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.b, SessionID: r.deepseek, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='test' AND after_merge=0`, c.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != mergerTaskOwner {
		t.Fatalf("test task owner after merger start = %q, want %q", owner, mergerTaskOwner)
	}
}

// needsPush asks for the push plan the way the daemon does on machine, for a
// worker whose caller is callerSession.
func needsPush(t *testing.T, r *deliveryRig, machine, callerSession string) bool {
	t.Helper()
	got, err := deliveryCall[protocol.DeliveryNeedsPushResult](r, machine, protocol.FrameDeliveryNeedsPush, protocol.DeliveryNeedsPushRequest{Project: "demo", CallerSessionID: callerSession})
	if err != nil {
		t.Fatal(err)
	}
	return got.NeedsPush
}

// The merger on machine b owns the test step. The worker and its caller are on
// machine a, so the branch must reach origin.
func TestDeliveryPushForRemoteMergerStep(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.b, SessionID: r.deepseek, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if !needsPush(t, r, r.a, r.caller) {
		t.Fatal("the merger on another machine owns the test step, but the branch is not pushed")
	}
}

// Without a merger on another machine, the branch stays local.
func TestDeliveryPushSkipsLocalOrMissingMerger(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: protocol.StepTest, Node: "@merger"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if needsPush(t, r, r.a, r.caller) {
		t.Fatal("no merger is configured, but the branch is pushed")
	}
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet, Configure: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	if needsPush(t, r, r.a, r.caller) {
		t.Fatal("the merger runs on the worker's machine, but the branch is pushed")
	}
}
