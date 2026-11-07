package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOLeadSetProposal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Version = "v0.4.145" }})
	cli := cliClient(t, n)
	eventually(t, "owner", func() bool { return n.d.ownerSessionID() != "" })
	if _, err := g.gw.Store().AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/work/demo"}}}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	cli.mustCall("team_write", map[string]any{"action": "create", "team_id": "alpha", "name": "Alpha"})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"repo", "team"} {
		id := "demo"
		if kind == "team" {
			id = "alpha"
		}
		raw := json.RawMessage(fmt.Sprintf(`{"action":"lead_set","ref":{"kind":%q,"id":%q},"lead":{"kind":"agent","id":"demo"}}`, kind, id))
		result, err := n.d.runCTOToolRaw(context.Background(), "graph_change", raw)
		if err != nil {
			t.Fatalf("%s proposal: %v", kind, err)
		}
		assertCTOImmediate(t, result)
		graph, err := g.gw.Store().GraphForRef(&protocol.GraphRef{Kind: kind, ID: id})
		if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Lead == nil || graph.Nodes[0].Lead.ID != "demo" {
			t.Fatalf("%s: %+v %v", kind, graph, err)
		}
	}
}
