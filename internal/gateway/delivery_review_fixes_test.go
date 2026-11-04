package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Owner dismisses a merge approval, then the caller reports an unrelated step on
// the same revision. Does the dismissed item stay dismissed?
func TestDeliveryRegressionDismissedApprovalReopens(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/dismissed", commitA)).Change.Change.ID
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("approval inbox: %+v, %v", items, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	// An unrelated step of the same revision is reported next.
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	items, err = r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("after report: %+v, %v", items, err)
	}
	if items[0].Status != "dismissed" {
		t.Fatalf("dismissed approval reopened after an unrelated report: status=%q", items[0].Status)
	}
}

// A change closed while an owner approval is open: what happens to the item?
func TestDeliveryRegressionClosedChangeLeavesApprovalItem(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/closed", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA})
	items, err := r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for _, item := range items {
		if item.Status == "open" && item.Delivery != nil {
			open++
		}
	}
	if open != 0 {
		t.Fatalf("%d delivery inbox items stay open after the change was closed", open)
	}
}

// A session that is not the caller reports a caller-owned step in a pipeline
// with no node-owned step (so no durable task row exists).
func TestDeliveryRegressionPeerReportsCallerStep(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("feature/peer", commitA)).Change.Change.ID
	res, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	if err == nil {
		t.Fatalf("a session that is not the caller reported the caller's step: by=%s", res.Evidence.By)
	}
}

// An unclaimed task for a step whose owner is reassigned: does the task move?
func TestDeliveryRegressionUnclaimedTaskKeepsOldOwner(t *testing.T) {
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
	if err := r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='accept' AND completed=0`, id).Scan(&owner); err != nil {
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

// A new revision of the same change: does the old revision's open approval stay?
func TestDeliveryRegressionStaleRevisionApprovalStaysOpen(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	r.submit(r.a, r.workerSubmit("feature/rev", commitA))
	r.submit(r.a, r.workerSubmit("feature/rev", commitB))
	items, err := r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for _, item := range items {
		if item.Status == "open" && item.Delivery != nil {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("%d open approval items after a new revision; want only the current one", open)
	}
}

// A node task queued, then the step is reassigned to the person. Does the old
// node keep the step?
func TestDeliveryRegressionPersonReassignmentBypassedByQueuedTask(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("first", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "first"}, "owner"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/person-move", commitA)).Change.Change.ID
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: "accept", Node: "@owner", Expect: "first"}, "owner"); err != nil {
		t.Fatal(err)
	}
	var owner string
	_ = r.s.db.QueryRow(`SELECT owner_node FROM delivery_step_tasks WHERE change_id=? AND step='accept' AND completed=0`, id).Scan(&owner)
	if owner == "first" {
		t.Fatalf("the old node still owns accept after the step moved to the person")
	}
	items, err := r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Delivery != nil && item.Delivery.Step == protocol.StepAccept {
			found = true
		}
	}
	if !found {
		t.Fatal("no owner inbox item after the step moved to the person")
	}
}

// The caller owns the steps but runs on another machine than the worker.
func TestDeliveryRegressionRemoteCallerNotInPushPlan(t *testing.T) {
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
	needs, err := r.s.DeliveryNeedsPush("demo", r.a)
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Fatal("the caller owns the steps on another machine, but the branch is not pushed")
	}
}

// The restart path: backfill runs at every OpenStore.
func TestDeliveryRegressionDismissedApprovalReopensOnBackfill(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	r.submit(r.a, r.workerSubmit("feature/restart", commitA))
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("approval inbox: %+v, %v", items, err)
	}
	if _, err := r.s.AnswerInbox(items[0].ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.backfillDeliveryTasks(); err != nil {
		t.Fatal(err)
	}
	items, err = r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("after backfill: %+v, %v", items, err)
	}
	if items[0].Status != "dismissed" {
		t.Fatalf("dismissed approval reopened at startup: status=%q", items[0].Status)
	}
}

// A node reports needs_owner, then the owner's decision comes back to the same
// node on the same commit. Can the step be reported again?
func TestDeliveryRegressionNeedsOwnerStepCannotBeReportedAgain(t *testing.T) {
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
	if _, err := r.report(r.b, protocol.StepReportRequest{SessionID: sid, ChangeID: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatalf("follow-up pass refused: %v", err)
	}
	// Is the step offered again, or does it wait for a new revision forever?
	for _, step := range r.get(id).Steps {
		if step.StepRef == ref {
			t.Logf("step %s state %s note %q", ref, step.State, step.Note)
		}
	}
}
