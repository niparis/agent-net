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
		`{"action":"agent_create","name":"agent"}`,
	} {
		if err := mcpserver.ValidateCTOActionSchema("graph_change", json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
		if _, _, _, err := ctoConcreteCall("graph_change", json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"action":"repo_archive","ref":{"kind":"repo","id":"repo"}}`,
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

// Archive and tombstone need the owner's words even while the CTO drives the
// graph. With his words they run at once, land in the CTO's history as the
// owner's words actor, and the last one is undone like any CTO change.
func TestCTOGraphLifecycleNeedsOwnerWordsEvenInWindow(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Version = "v0.4.145" }})
	cli := cliClient(t, n)
	ctx := context.Background()
	waitGraphCutover(t, g)
	n.d.ctoGraph.mu.Lock()
	n.d.ctoGraph.toggle(time.Now())
	n.d.ctoGraph.mu.Unlock()
	words := "archive review-repo and tombstone review-agent"
	ctoOwnerSays(t, n.d, "please "+words)
	for _, tc := range []struct{ action, args string }{
		{"repo_create", `{"name":"review-repo"}`},
		{"agent_create", `{"name":"review-agent"}`},
		{"repo_archive", `{"ref":{"kind":"repo","id":"review-repo"}}`},
		{"agent_tombstone", `{"ref":{"kind":"agent","id":"review-agent"}}`},
	} {
		if tc.action == "repo_archive" || tc.action == "agent_tombstone" {
			if _, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs(tc.action, tc.args)); err == nil || !strings.Contains(err.Error(), "needs the owner's approval") {
				t.Fatalf("%s ran without the owner's words: %v", tc.action, err)
			}
			tc.args = strings.TrimSuffix(tc.args, "}") + `,"owner_words":"` + words + `"}`
		}
		result, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs(tc.action, tc.args))
		if err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		assertCTOImmediate(t, result)
	}
	for _, ref := range []protocol.GraphRef{{Kind: "repo", ID: "review-repo"}, {Kind: "agent", ID: "review-agent"}} {
		if graph, err := g.gw.Store().GraphForRef(&ref); err != nil || len(graph.Nodes) != 0 {
			t.Fatalf("%s still active: %+v %v", ref.ID, graph, err)
		}
	}
	var history []protocol.CTOGraphChange
	if err := json.Unmarshal(cli.mustCall("cto_graph_history", nil), &history); err != nil || len(history) != 4 {
		t.Fatalf("history %+v: %v", history, err)
	}
	result, err := n.d.runCTOToolRaw(ctx, "graph_change", ctoGraphArgs("undo", fmt.Sprintf(`{"id":%d}`, history[3].ID)))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
	if graph, err := g.gw.Store().GraphForRef(&protocol.GraphRef{Kind: "agent", ID: "review-agent"}); err != nil || len(graph.Nodes) != 1 {
		t.Fatalf("undo did not bring the agent back: %+v %v", graph, err)
	}
}
