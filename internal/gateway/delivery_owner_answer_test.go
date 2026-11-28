package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ownerSessionOn makes an online owner pseudo-session on a node, the session
// the gateway checks for an owner answer.
func ownerSessionOn(t *testing.T, s *Store, node string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// An owner merge approval reaches the chief as a request that ends with the
// CTO call that completes it on the owner's quoted words (decision 0196).
func TestOwnerApprovalRequestNamesDeliveryOwnerAnswer(t *testing.T) {
	r := newDeliveryRig(t)
	chief, err := r.s.UpsertSession(r.a, protocol.SessionInfo{Harness: "claude", NativeID: "chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET online=1 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/owner-answer", commitA)).Change.Change.ID

	var body string
	for _, m := range messagesTo(t, r.s, chief) {
		if strings.Contains(m.body, id) && strings.Contains(m.body, "Approve merge") {
			body = m.body
		}
	}
	if body == "" {
		t.Fatalf("no owner approval request to the chief: %+v", messagesTo(t, r.s, chief))
	}
	call := ctoOpCallText(protocol.OpDeliveryOwnerAnswer, map[string]any{
		"change": id, "step": protocol.StepApprove, "for": protocol.StepMerge, "commit": commitA,
		"result": protocol.StepResultPass, "owner_words": "<quote the owner's words>"})
	if !strings.HasSuffix(body, call) {
		t.Fatalf("request does not end with %q:\n%s", call, body)
	}
}

// The CTO completes an owner merge approval on the owner's quoted words. The
// evidence records the owner, no Desk item is stored, and the approval passes.
func TestDeliveryOwnerAnswerCompletesAnOwnerApproval(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/owner-complete", commitA)).Change.Change.ID
	ref := protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}
	words := "go ahead and merge it"
	owner := ownerSessionOn(t, r.s, r.a)
	result, err := deliveryCall[protocol.StepReportResult](r, r.a, protocol.FrameDeliveryOwnerAnswer, protocol.DeliveryOwnerAnswerFrame{
		Request: protocol.DeliveryOwnerAnswerRequest{Change: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA, OwnerWords: words},
		Owner:   owner, Session: r.caller, By: "cto", WordsVerified: true,
	})
	if err != nil {
		t.Fatalf("owner answer: %v", err)
	}
	if result.Evidence.By != protocol.EvidenceByOwner || result.Evidence.ReportedBy != r.caller || result.Evidence.Commit != commitA {
		t.Fatalf("evidence: %+v", result.Evidence)
	}
	if got := r.get(id).Change.State; got != protocol.ChangeOpen {
		t.Fatalf("state after owner approval = %s", got)
	}
	if st := stepStatus(t, r.get(id), ref); st.State != protocol.StepStatePassed || st.Evidence == nil {
		t.Fatalf("approval step after the owner answer: %+v", st)
	}
}

// Without verified owner words, an @owner step is still refused, so the
// existing writer approval rule keeps its force.
func TestDeliveryOwnerAnswerNeedsVerifiedOwnerWords(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	id := r.submit(r.a, r.workerSubmit("feature/owner-no-words", commitA)).Change.Change.ID
	ref := protocol.StepRef{Step: protocol.StepApprove, For: protocol.StepMerge}
	owner := ownerSessionOn(t, r.s, r.a)
	// A plain step report cannot claim the owner's approval, even with words.
	if _, err := r.report(r.a, protocol.StepReportRequest{SessionID: r.caller, ChangeID: id, StepRef: ref,
		Result: protocol.StepResultPass, Commit: commitA, DoneBy: protocol.EvidenceByOwner}); err == nil {
		t.Fatal("a plain step report completed an @owner step")
	}
	// The owner answer frame refuses unverified words.
	for _, frame := range []protocol.DeliveryOwnerAnswerFrame{
		{Request: protocol.DeliveryOwnerAnswerRequest{Change: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA, OwnerWords: "go ahead"},
			Owner: owner, Session: r.caller, By: "cto"},
		{Request: protocol.DeliveryOwnerAnswerRequest{Change: id, StepRef: ref, Result: protocol.StepResultPass, Commit: commitA},
			Owner: owner, Session: r.caller, By: "cto", WordsVerified: true},
	} {
		if _, err := deliveryCall[protocol.StepReportResult](r, r.a, protocol.FrameDeliveryOwnerAnswer, frame); err == nil {
			t.Fatalf("owner answer without verified words was accepted: %+v", frame)
		}
	}
}
