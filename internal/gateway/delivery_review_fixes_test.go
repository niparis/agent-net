package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session that is not the caller reports a caller-owned step in a pipeline
// with no node-owned step (so no durable task row exists).
func TestDeliveryPeerCannotReportCallerStep(t *testing.T) {
	r := newDeliveryRig(t)
	r.moveToAgent(r.sonnet, "peer")
	id := r.submit(r.a, r.workerSubmit("feature/peer", commitA)).Change.Change.ID
	res, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	if err == nil {
		t.Fatalf("a session that is not the caller reported the caller's step: by=%s", res.Evidence.By)
	}
}

// An unclaimed task for a step whose owner is reassigned: does the task move?
func TestDeliveryUnclaimedTaskMovesToNewOwner(t *testing.T) {
	r := newDeliveryRig(t)
	for _, node := range []string{"first", "second"} {
		if err := r.s.AddArchitect(node, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "first"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/handover", commitA)).Change.Change.ID
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "second", Expect: "first"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='accept'`, id).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "second" {
		t.Fatalf("unclaimed task still owned by %q after the owner moved to second", owner)
	}
	for _, step := range r.get(id).Steps {
		if step.Step == protocol.StepAccept && step.Owner.Node != "second" {
			t.Fatalf("change view shows old owner %q", step.Owner.Node)
		}
	}
	_ = strings.TrimSpace
}

// The caller owns the steps but runs on another machine than the worker.
func TestDeliveryRemoteCallerRequiresPush(t *testing.T) {
	r := newDeliveryRig(t)
	callerB, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "claude", NativeID: "caller-b", Model: "claude-opus-5-5", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	workerA, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "worker-a", Model: "claude-opus-5-5", Repo: protocol.RepoInfo{Project: "demo"},
		Worker: &protocol.WorkerInfo{ID: "w_9", Owner: callerB}})
	if err != nil {
		t.Fatal(err)
	}
	q := protocol.ChangeSubmitRequest{SessionID: callerB, Project: "demo", Branch: "feature/remote-caller", TargetBranch: "main", BaseCommit: commitBase, HeadCommit: commitA,
		Origin: protocol.ChangeOrigin{Kind: protocol.OriginWorker, WorkerID: "w_9", SessionID: workerA, Model: "claude-opus-5-5"}}
	if _, err := deliveryCall[protocol.ChangeSubmitResult](r, r.a, protocol.FrameChangeSubmit, q); err != nil {
		t.Fatal(err)
	}
	needs, err := r.s.DeliveryNeedsPush("demo", r.a, callerB)
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Fatal("the caller owns the steps on another machine, but the branch is not pushed")
	}
}

// A node reports needs_owner, then the owner's decision comes back to the same
// node on the same commit. Can the step be reported again?
func TestDeliveryNeedsOwnerStepAcceptsOwnerFollowUp(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("merger", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "merger", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/needs-owner", commitA)).Change.Change.ID
	ref := stepAccept
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: ref, Result: protocol.StepResultNeedsOwner, Commit: commitA, Note: "is this in scope?"}); err != nil {
		t.Fatal(err)
	}
	other, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "other-merger", Repo: protocol.RepoInfo{Project: "merger"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: other, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA}); err == nil {
		t.Fatal("another session reported a claimed needs_owner step")
	}
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatalf("follow-up pass refused: %v", err)
	}
}

func TestDeliveryReassignmentPreservesClaim(t *testing.T) {
	r := newDeliveryRig(t)
	for _, node := range []string{"first", "second"} {
		if err := r.s.AddArchitect(node, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "first"}, "owner"); err != nil {
		t.Fatal(err)
	}
	sid, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "first", Repo: protocol.RepoInfo{Project: "first"}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/claimed", commitA)).Change.Change.ID
	if _, err := r.s.ClaimStep(r.b, protocol.StepClaimRequest{SessionID: sid, ChangeID: id, StepRef: stepAccept, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "second", Expect: "first"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryReassignmentBackToNode(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("first", "owner"); err != nil {
		t.Fatal(err)
	}
	q := protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "first"}
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/return", commitA)).Change.Change.ID
	q.Node, q.Expect = "@owner", "first"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	q.Node, q.Expect = "first", "@owner"
	if err := r.s.SetDeliveryOwner(q, "owner"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND step='accept' AND owner_node='first'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active node task = %d, %v", count, err)
	}
}
