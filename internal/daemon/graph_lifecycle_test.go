package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/mcpserver"
)

func TestCTOGraphLifecycleProposalSchema(t *testing.T) {
	for _, raw := range []string{
		`{"action":"repo_create","name":"repo"}`,
		`{"action":"repo_archive","ref":{"kind":"repo","id":"repo"}}`,
		`{"action":"agent_create","name":"agent"}`,
		`{"action":"agent_tombstone","ref":{"kind":"agent","id":"agent"}}`,
	} {
		if err := mcpserver.ValidateCTOActionSchema("graph_change", json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
		if _, _, _, err := ctoConcreteCall("graph_change", json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestCTOGraphLifecycleRequiresProposalEvenInWindow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Version = "v0.4.145" }})
	cli := cliClient(t, n)
	ctx := context.Background()
	waitGraphCutover(t, g)
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.toggle(time.Now())
	n.d.ctoGraph.mu.Unlock()
	for _, tc := range []struct{ action, args string }{
		{"repo_create", `{"name":"review-repo"}`},
		{"agent_create", `{"name":"review-agent"}`},
		{"repo_archive", `{"ref":{"kind":"repo","id":"review-repo"}}`},
		{"agent_tombstone", `{"ref":{"kind":"agent","id":"review-agent"}}`},
	} {
		result, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs(tc.action, tc.args))
		if err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		proposal, ok := result.(string)
		if !ok || !strings.HasPrefix(proposal, "proposal p_") {
			t.Fatalf("%s applied without approval: %v", tc.action, result)
		}
		id := strings.Fields(proposal)[1]
		cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{ID: id, Action: "a"})
	}
	var history []protocol.CTOGraphChange
	if err := json.Unmarshal(cli.mustCall("cto_graph_history", nil), &history); err != nil || len(history) != 4 {
		t.Fatalf("history %+v: %v", history, err)
	}
	result, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs("undo", fmt.Sprintf(`{"id":%d}`, history[3].ID)))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := result.(string); !ok || !strings.HasPrefix(s, "proposal p_") {
		t.Fatalf("undo applied without approval: %v", result)
	}
}
