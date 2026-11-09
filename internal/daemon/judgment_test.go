package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestJudgmentAddOperatorMethodAndFind(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 1. Call judgment_add via operator method on the cli client
	req := protocol.JudgmentAddRequest{
		Kind: protocol.StandingKindCorrection,
		Text: "Always run go vet before handing back.",
		Area: "agent-net",
		Ref:  "ask_1",
	}
	var added protocol.StandingOrder
	mustJSON(t, n.cli.mustCall(protocol.OpJudgmentAdd, req), &added)
	if added.Kind != protocol.StandingKindCorrection {
		t.Errorf("expected kind %q, got %q", protocol.StandingKindCorrection, added.Kind)
	}
	if added.Area != "agent-net" {
		t.Errorf("expected area %q, got %q", "agent-net", added.Area)
	}
	if added.Ref != "ask_1" {
		t.Errorf("expected ref %q, got %q", "ask_1", added.Ref)
	}

	// 2. Also test CTO calling judgment_add
	ctoReq := map[string]any{
		"kind": protocol.StandingKindRule,
		"text": "Never commit credentials or tokens.",
		"area": "ancient-standards",
	}
	raw, _ := json.Marshal(ctoReq)
	ctoOut, err := n.d.runCTOToolRaw(context.Background(), "judgment_add", raw)
	if err != nil {
		t.Fatalf("CTO judgment_add failed: %v", err)
	}
	b, _ := json.Marshal(ctoOut)
	var ctoAdded protocol.StandingOrder
	if err := json.Unmarshal(b, &ctoAdded); err != nil {
		t.Fatal(err)
	}
	if ctoAdded.Kind != protocol.StandingKindRule {
		t.Errorf("expected rule, got %q", ctoAdded.Kind)
	}

	// 3. Find via judgments_find daemon method
	findReq := protocol.JudgmentsFindRequest{
		Query: "credentials",
		Area:  "ancient-standards",
	}
	findBytes := n.cli.mustCall("judgments_find", findReq)
	var findResult struct {
		Items []protocol.StandingOrder `json:"items"`
		Total int                      `json:"total"`
	}
	if err := json.Unmarshal(findBytes, &findResult); err != nil {
		t.Fatal(err)
	}
	if len(findResult.Items) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(findResult.Items))
	}
	if !strings.Contains(findResult.Items[0].Text, "credentials") {
		t.Errorf("unexpected text: %q", findResult.Items[0].Text)
	}
}
