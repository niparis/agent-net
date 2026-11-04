package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A merge step assigned to the person: does the Inbox approval path enforce
// the merge-ready gate?
func TestInboxMergeApprovalRequiresReady(t *testing.T) {
	r := newDeliveryRig(t)
	id := r.submit(r.a, r.workerSubmit("feature/inbox-merge", commitA)).Change.Change.ID
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultPass, Commit: commitA})
	r.mustReport(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepTest}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldSuite: protocol.SuiteChanged}})
	// The owner assigns the merge step to themselves once it is ready.
	if err := r.s.SetDeliveryOwner(protocol.DeliveryOwnerSetRequest{Project: "demo", Step: protocol.StepMerge, Node: "@owner"}, "owner"); err != nil {
		t.Fatalf("assign merge to person: %v", err)
	}
	items, err := r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	mergeItem := ""
	for _, it := range items {
		if it.Status == "open" && it.Delivery != nil && it.Delivery.Step == protocol.StepMerge {
			mergeItem = it.ID
		}
	}
	if mergeItem == "" {
		t.Fatalf("no open merge approval: %+v", items)
	}
	// The caller withdraws accept after the item exists.
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: stepAccept, Result: protocol.StepResultFail, Commit: commitA, Note: "found a bug"}); err != nil {
		t.Fatalf("accept fail: %v", err)
	}
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: protocol.StepRef{Step: protocol.StepMerge}, Result: protocol.StepResultPass, Commit: commitA, Fields: map[string]string{protocol.FieldMergedCommit: commitMerged}}); err == nil {
		t.Fatal("step report merged although the pipeline says it is not ready")
	}
	answer := "approve " + string(mustJSON(t, map[string]string{protocol.FieldMergedCommit: commitMerged}))
	if _, err := r.s.AnswerInbox(mergeItem, answer); err == nil || !strings.Contains(err.Error(), "merge is not ready:") {
		t.Fatalf("expected merge readiness refusal, got %v", err)
	}
	items, err = r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == mergeItem && item.Status != "open" {
			t.Fatalf("approval closed: %+v", item)
		}
	}
	if got := r.get(id).Change.State; got == protocol.ChangeMerged {
		t.Fatalf("the Inbox approval merged a change whose pipeline is not ready")
	}
}

// Owner dismisses an approval, then turns the approval rule off and on again
// for the same revision. Does the dismissal stay?
func TestOwnerDismissSurvivesRuleToggle(t *testing.T) {
	r := newDeliveryRig(t)
	write := func(value, expect string) {
		t.Helper()
		c := SettingsChange{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(value)}
		if expect != "" {
			c.Expect = json.RawMessage(expect)
		}
		if _, err := r.s.WriteSettings([]SettingsChange{c}, "owner", "test"); err != nil {
			t.Fatalf("write %s: %v", value, err)
		}
	}
	write(`"ask"`, "")
	r.submit(r.a, r.workerSubmit("feature/toggle", commitA))
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("inbox: %+v, %v", items, err)
	}
	id := items[0].ID
	if _, err := r.s.AnswerInbox(id, "dismiss"); err != nil {
		t.Fatal(err)
	}
	write(`"full"`, `"ask"`)
	write(`"ask"`, `"full"`)
	items, err = r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == id && item.Status == "open" {
			t.Fatalf("the owner's dismissed approval reopened after the rule was turned off and on")
		}
	}
}
