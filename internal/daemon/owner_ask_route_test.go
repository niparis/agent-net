package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// agent_owner_ask is one daemon method for every harness. The daemon, not the
// harness, picks the ask kind (decision in one place, principle 18): a
// question is a Desk ask, not an explicit wait.
func TestOwnerAskRoutesByKind(t *testing.T) {
	t.Parallel()
	n, lead, member, _, memberID, _ := forgeNodes(t)

	// A question makes an ask item for the owner, the same as ask_question.
	raw, err := lead.call(protocol.MethodOwnerAsk, map[string]any{"kind": "question", "text": "Spend money?", "recommendation": "no", "options": []string{"yes", "no"}})
	if err != nil {
		t.Fatal(err)
	}
	var q struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &q); err != nil || q.Item.ID == "" || q.Item.Kind != "question" {
		t.Fatalf("question answer: %s %v", raw, err)
	}

	// An approval and an idea reach their own kinds.
	raw, err = lead.call(protocol.MethodOwnerAsk, map[string]any{"kind": "approval", "text": "Delete the branch?", "recommendation": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.Item.Kind != "approval" {
		t.Fatalf("approval answer: %s %v", raw, err)
	}
	raw, err = lead.call(protocol.MethodOwnerAsk, map[string]any{"kind": "idea", "text": "Add a map", "recommendation": "later"})
	if err != nil {
		t.Fatal(err)
	}
	var i struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &i); err != nil || i.Item.Kind != "feature_idea" {
		t.Fatalf("idea answer: %s %v", raw, err)
	}

	// replaces with no kind retires a question.
	raw, err = lead.call(protocol.MethodOwnerAsk, map[string]any{"replaces": q.Item.ID})
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Item protocol.InboxItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Item.Status != "replaced" {
		t.Fatalf("replaces answer: %s %v", raw, err)
	}

	if _, err := lead.call(protocol.MethodOwnerAsk, map[string]any{"kind": "wish", "text": "x"}); err == nil || !strings.Contains(err.Error(), "kind must be question, approval or idea") {
		t.Fatalf("bad kind error: %v", err)
	}

	// A team that sends questions to its lead takes a question from owner_ask
	// the same way it takes one from ask_question.
	raw, err = member.call(protocol.MethodOwnerAsk, map[string]any{"kind": "question", "text": "Which schema?"})
	if err != nil || !strings.Contains(string(raw), "sends questions to its lead") {
		t.Fatalf("member question: %s %v", raw, err)
	}
	if w := waitingOf(t, n, memberID); w != nil {
		t.Fatalf("member waits for its user: %+v", w)
	}
}
