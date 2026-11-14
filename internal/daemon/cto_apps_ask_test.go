package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOAppAskCannotApproveItsOwnConsent(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	seedAppPending(t, g)
	result, err := n.d.runCTOTool(ctx, "mcp_change", json.RawMessage(`{"action":"app_ask","code":"ABC234","kind":"bot","name":"Fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var resultBody struct {
		Result struct {
			Data protocol.Ask `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resultBody); err != nil || resultBody.Result.Data.ID == "" {
		t.Fatalf("ask: %s %v", raw, err)
	}
	ask := resultBody.Result.Data
	args, _ := json.Marshal(map[string]string{"kind": "inbox", "id": ask.ID, "answer": "approve"})
	if _, err := n.d.runCTOTool(ctx, "answer", args); err == nil || !strings.Contains(err.Error(), "owner's own answer") {
		t.Fatalf("CTO consent: %v", err)
	}
	cli := cliClient(t, n)
	cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": "approve"})
	asks, err := g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.ID == ask.ID {
			t.Fatalf("approved ask stayed open: %+v", a)
		}
	}
	apps, err := g.gw.Store().ListApps()
	if err != nil || len(apps) != 1 || apps[0].Name != "Fixture" {
		t.Fatalf("app approval: %+v %v", apps, err)
	}
}
