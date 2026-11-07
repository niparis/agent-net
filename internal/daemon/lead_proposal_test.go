package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
		proposal, ok := result.(string)
		if !ok || !strings.HasPrefix(proposal, "proposal p_") {
			t.Fatalf("not proposed: %v", result)
		}
		cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{ID: strings.Fields(proposal)[1], Action: "a"})
		graph, err := g.gw.Store().GraphForRef(&protocol.GraphRef{Kind: kind, ID: id})
		if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Lead == nil || graph.Nodes[0].Lead.ID != "demo" {
			t.Fatalf("%s: %+v %v", kind, graph, err)
		}
	}
}

func TestCTOSessionLeadSetProposal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Version = "v0.4.145" }})
	cli := cliClient(t, n)
	eventually(t, "owner", func() bool { return n.d.ownerSessionID() != "" })
	if _, err := g.gw.Store().AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/work/demo"}}}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	sid, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "lead-session", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(fmt.Sprintf(`{"action":"lead_set","ref":{"kind":"repo","id":"demo"},"lead":{"kind":"session","id":%q}}`, sid))
	result, err := n.d.runCTOToolRaw(context.Background(), "graph_change", raw)
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := result.(string)
	if !ok || !strings.HasPrefix(proposal, "proposal p_") {
		t.Fatalf("not proposed: %v", result)
	}
	cli.mustCall("proposal_answer", protocol.CTOProposalAnswer{ID: strings.Fields(proposal)[1], Action: "a"})
	graph, err := g.gw.Store().GraphForRef(&protocol.GraphRef{Kind: "repo", ID: "demo"})
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Lead == nil || *graph.Nodes[0].Lead != (protocol.GraphRef{Kind: "session", ID: sid}) {
		t.Fatalf("lead: %+v %v", graph, err)
	}
}
