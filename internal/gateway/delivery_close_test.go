package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A change whose next step is merge, owned by the merger: its caller can still
// close it. The evidence stays, the merger is told, and no task stays pending.
func TestCallerClosesChangeAtMergerStep(t *testing.T) {
	r := setupMergerRig(t)
	id := r.submit(r.a, r.workerSubmit("feature/replaced", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	merge := protocol.StepRef{Step: protocol.StepMerge}
	if _, err := r.s.ClaimStep(r.a, protocol.StepClaimRequest{SessionID: r.sonnet, ChangeID: id, StepRef: merge, Commit: commitA}); err != nil {
		t.Fatalf("merger claim: %v", err)
	}
	for _, st := range r.get(id).Steps {
		if st.StepRef == merge && st.State != protocol.StepStateRunning {
			t.Fatalf("merge step %+v, want running under the merger", st)
		}
	}

	res := r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: merge, Result: protocol.StepResultClose, Commit: commitA, Note: "replaced by the rebased change ch_e727"})
	if res.Change.Change.State != protocol.ChangeStopped || len(res.Change.Next) != 0 {
		t.Fatalf("after caller close %+v", res.Change)
	}

	// The close keeps the earlier evidence and records the reason.
	v := r.get(id)
	steps := map[string]protocol.StepStatus{}
	for _, st := range v.Steps {
		steps[st.String()] = st
	}
	for _, ref := range []protocol.StepRef{stepAccept, {Step: protocol.StepTest}} {
		if st := steps[ref.String()]; st.State != protocol.StepStatePassed {
			t.Fatalf("%s evidence lost: %+v", ref, st)
		}
	}
	closed, ok := steps[merge.String()]
	if !ok || closed.State != protocol.StepStateClosed || closed.Evidence == nil ||
		!strings.Contains(closed.Evidence.Note, "replaced by the rebased change") {
		t.Fatalf("close evidence %+v", closed)
	}

	// No step task stays pending, so a reminder has nothing to repeat.
	var pending int
	if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM delivery_step_tasks WHERE change_id=? AND completed=0`, id).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending step tasks after close = %d, %v", pending, err)
	}

	// The merger had claimed the step: it is told to drop the change.
	var body string
	if err := r.s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND body LIKE ? ORDER BY created_at DESC LIMIT 1`, r.sonnet, "%"+id+"%").Scan(&body); err != nil {
		t.Fatalf("merger notice: %v", err)
	}
	if !strings.Contains(body, "closed") || !strings.Contains(body, id) {
		t.Fatalf("merger notice %q", body)
	}
}

// A session that is not the change's caller cannot close it. The owner may.
func TestCallerCloseRefusedForNonCaller(t *testing.T) {
	r := newDeliveryRig(t)
	r.moveToAgent(r.sonnet, "peer")
	id := r.submit(r.a, r.workerSubmit("feature/peer-close", commitA)).Change.Change.ID
	_, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA, Note: "not mine"})
	if err == nil || !strings.Contains(err.Error(), "caller") {
		t.Fatalf("a session that is not the caller closed the change: %v", err)
	}
	if got := r.get(id).Change.State; got != protocol.ChangeOpen {
		t.Fatalf("state after refused close = %s", got)
	}

	owner, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID, Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.report(r.a, protocol.StepReportRequest{SessionID: owner, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA, Note: "the owner does not want it"})
	if err != nil {
		t.Fatalf("owner close refused: %v", err)
	}
	if res.Change.Change.State != protocol.ChangeStopped {
		t.Fatalf("owner close state = %s", res.Change.Change.State)
	}
}

// After a close, the reminder and stuck passes send nothing for the change.
func TestCallerCloseStopsStepReminders(t *testing.T) {
	r := setupMergerRig(t)
	id := r.submit(r.a, r.workerSubmit("feature/no-reminder", commitA)).Change.Change.ID
	r.g.dispatchStepTasks(context.Background())
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.sonnet, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultClose, Commit: commitA, Note: "closed instead of merged"})

	count := func() int {
		var n int
		if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE body LIKE ?`, "%"+id+"%").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	afterClose := count()
	r.clk.t = r.clk.t.Add(13 * time.Hour)
	r.g.dispatchStepTasks(context.Background())
	if got := count(); got != afterClose {
		t.Fatalf("messages for the closed change grew from %d to %d after the reminder pass", afterClose, got)
	}
	for _, pattern := range []string{"%step task reminder%", "%has waited%nobody claimed it%"} {
		var n int
		if err := r.s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE body LIKE ?`, pattern).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s messages = %d, %v", pattern, n, err)
		}
	}
}

// Closing a change that waits for the owner's written approval dismisses the
// Inbox item.
func TestCallerCloseClosesApprovalInbox(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/no-approval", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultClose, Commit: commitA, Note: "superseded"})
	items, err := r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status == "open" && item.Delivery != nil {
			t.Fatalf("approval item stays open after the caller closed the change: %+v", item)
		}
	}
}

// A close must say why.
func TestCallerCloseNeedsNote(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("feature/no-note", commitA)).Change.Change.ID
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultClose, Commit: commitA}); err == nil || !strings.Contains(err.Error(), "note") {
		t.Fatalf("close without a note: %v", err)
	}
	if got := r.get(id).Change.State; got != protocol.ChangeOpen {
		t.Fatalf("state after close without a note = %s", got)
	}
}
