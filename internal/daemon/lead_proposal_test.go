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

func TestCTOSessionLeadSetProposal(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Version = "v0.4.145" }})
	eventually(t, "owner", func() bool { return n.d.ownerSessionID() != "" })
	if _, err := g.gw.Store().AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/work/demo"}}}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	sid, err := g.gw.Store().UpsertSession(n.d.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "lead-session", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(fmt.Sprintf(`{"action":"lead_set","ref":{"kind":"repo","id":"demo"},"lead":{"kind":"session","id":%q}}`, sid))
	result, err := n.d.runCTOToolRaw(context.Background(), "graph_change", raw)
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
	graph, err := g.gw.Store().GraphForRef(&protocol.GraphRef{Kind: "repo", ID: "demo"})
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Lead == nil || *graph.Nodes[0].Lead != (protocol.GraphRef{Kind: "session", ID: sid}) {
		t.Fatalf("lead: %+v %v", graph, err)
	}
}

func TestScopeSetThroughCTOAndOwner(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.Version = "v0.4.145" }})
	cli := cliClient(t, n)
	eventually(t, "owner", func() bool { return n.d.ownerSessionID() != "" })
	if _, err := g.gw.Store().AddProjects(n.d.cfg.NodeID, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/work/demo"}}}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored := func() []protocol.Scope {
		t.Helper()
		rows, err := g.gw.Store().NodeScopes("demo")
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	result, err := n.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"scope_set","node":"demo","kind":"review","over":"repo:demo","note":"own repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertCTOImmediate(t, result)
	if rows := stored(); len(rows) != 1 || rows[0].Kind != protocol.ScopeReview || rows[0].SetBy != "cto" {
		t.Fatalf("CTO scope_set: %+v", rows)
	}
	cli.mustCall(protocol.OpGraphUndo, map[string]any{})
	if rows := stored(); len(rows) != 0 {
		t.Fatalf("after undo: %+v", rows)
	}
	var row protocol.Scope
	raw := cli.mustCall(protocol.OpScopeSet, map[string]any{"node": "demo", "kind": "merge", "over": "repo:demo"})
	if err := json.Unmarshal(raw, &row); err != nil || row.Node != "demo" || row.Kind != protocol.ScopeMerge || row.Over != "repo:demo" || row.SetBy != "owner" {
		t.Fatalf("owner scope_set: %s %v", raw, err)
	}
	if _, err := cli.call(protocol.OpScopeSet, map[string]any{"node": "demo", "kind": "approve", "over": "repo:demo"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	raw = cli.mustCall(protocol.OpScopeRemove, map[string]any{"node": "demo", "kind": "merge", "over": "repo:demo"})
	if err := json.Unmarshal(raw, &row); err != nil || row.Kind != protocol.ScopeMerge || len(stored()) != 0 {
		t.Fatalf("owner scope_remove: %s %v", raw, err)
	}
}
