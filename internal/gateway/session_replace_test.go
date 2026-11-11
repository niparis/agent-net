package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// When a restart cannot keep the session ID (a new conversation), the old
// session's work moves to the new one in one transaction: the caller role of
// its running and queued workers, its open change and claimed step, and an
// undelivered request. The old record is marked replaced, not renamed.
func TestRestartMovesTheWorkWhenANewConversationGetsANewID(t *testing.T) {
	r := newDeliveryRig(t)
	// A shared pipeline gives the caller real step tasks it can claim.
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "reviews"}, "owner"); err != nil {
		t.Fatal(err)
	}
	// The caller quits. A new conversation registers and takes the name.
	id := r.submit(r.a, r.workerSubmit("feature/restart", commitA)).Change.Change.ID
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept,
		Result: protocol.StepResultPass, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	testStep := protocol.StepRef{Step: protocol.StepTest}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.caller, ChangeID: id, StepRef: testStep, Commit: commitA}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetSessionOffline(r.a, r.caller); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller-copy",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	// A request the old session has not been given yet.
	pending, _, err := r.s.StoreMessage(protocol.MsgSend{Sender: r.worker, Recipient: r.caller, Type: protocol.TypeRequest, Body: "the hand-back"})
	if err != nil {
		t.Fatal(err)
	}
	// A worker start that is still queued at the gateway.
	if err := r.s.TrackWorkerStart("ref-queued", r.a, r.caller, "demo", "claude", ""); err != nil {
		t.Fatal(err)
	}

	moved, err := r.s.ReplaceSession(r.a, r.caller, fresh)
	if err != nil || !moved {
		t.Fatalf("replace: moved=%v err=%v", moved, err)
	}

	// The running worker's caller.
	w, ok, err := r.s.Agent(r.worker)
	if err != nil || !ok || w.Worker == nil || w.Worker.Owner != fresh {
		t.Fatalf("the running worker still names the old caller: %+v %v", w, err)
	}
	// The queued worker start.
	var queued string
	if err := r.s.db.QueryRow(`SELECT owner FROM worker_starts WHERE ref='ref-queued'`).Scan(&queued); err != nil || queued != fresh {
		t.Fatalf("the queued worker still names the old caller: %q %v", queued, err)
	}
	// The open change and its claimed step: only the new session may report it.
	ch, err := r.s.GetChange(id)
	if err != nil || ch.Change.Caller != fresh {
		t.Fatalf("the open change still names the old caller: %+v %v", ch.Change, err)
	}
	var claimed string
	if err := r.s.db.QueryRow(`SELECT claimed_by FROM delivery_step_tasks WHERE change_id=? AND step='test' AND completed=0`, id).Scan(&claimed); err != nil || claimed != fresh {
		t.Fatalf("the claimed step still names the old caller: %q %v", claimed, err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: fresh, ChangeID: id, StepRef: testStep,
		Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}}); err != nil {
		t.Fatalf("the new session could not report the claimed step: %v", err)
	}
	// The undelivered request.
	msg, ok, err := r.s.Message(pending.ID)
	if err != nil || !ok || msg.To != fresh {
		t.Fatalf("the undelivered request still names the old session: %+v %v", msg, err)
	}

	// The old record is replaced, keeps its name, and says which session took
	// the work. It was not renamed to "demo 2" or "demo 3".
	before, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok {
		t.Fatalf("read the replaced session: %+v %v", before, err)
	}
	if before.ReplacedBy != fresh || before.Profile.Name != "demo" {
		t.Fatalf("the old record is not marked replaced by the new one: %+v", before)
	}
	after, ok, err := r.s.Agent(fresh)
	if err != nil || !ok || after.Profile.Name != "demo" {
		t.Fatalf("the new session did not keep the name: %+v %v", after, err)
	}
	// A later discovery report of the exited conversation must not rename the
	// replaced record.
	if _, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "caller", Status: "exited",
		Repo: protocol.RepoInfo{Root: "/code/demo", Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	again, ok, err := r.s.Agent(r.caller)
	if err != nil || !ok || again.Profile.Name != "demo" || again.ReplacedBy != fresh {
		t.Fatalf("the replaced record changed on a later report: %+v %v", again, err)
	}
}
