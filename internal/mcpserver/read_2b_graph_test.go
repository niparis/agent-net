package mcpserver

import (
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Graph nodes page with no overlap and no gap, and node reads one node with its
// edges.
func TestGraphReadPagesNodesAndReadsOneNode(t *testing.T) {
	f := &roFakeDaemon{}
	for i := 0; i < 60; i++ {
		f.graph.Nodes = append(f.graph.Nodes, protocol.PublicGraphNode{
			Name: fmt.Sprintf("n_%02d", i), Kind: "component", Maturity: "mature",
			Charter: &protocol.Charter{Purpose: "Own " + fmt.Sprintf("n_%02d", i)},
		})
	}
	f.graph.Total = 60
	f.graph.Edges = []protocol.PublicGraphEdge{{From: "n_00", To: "n_01", Kind: "depends", Mode: "advisor", Rule: "normal"}}
	cs := connect(t, f.call)

	var names []string
	cursor := ""
	for page := 0; page < 3; page++ {
		args := map[string]any{"limit": 25}
		if cursor != "" {
			args["cursor"] = cursor
		}
		out := roCall(t, cs, "agent_graph_read", args)
		nodes := out["nodes"].([]any)
		for _, node := range nodes {
			names = append(names, node.(map[string]any)["name"].(string))
		}
		cursor, _ = out["next_cursor"].(string)
		if page < 2 && cursor == "" {
			t.Fatalf("page %d has no next_cursor", page)
		}
	}
	if len(names) != 60 {
		t.Fatalf("three graph pages returned %d nodes, want 60", len(names))
	}
	for i, name := range names {
		if want := fmt.Sprintf("n_%02d", i); name != want {
			t.Fatalf("node %d = %s, want %s", i, name, want)
		}
	}

	one := roCall(t, cs, "agent_graph_read", map[string]any{"node": "n_00"})
	nodes := one["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["purpose"] != "Own n_00" {
		t.Fatalf("node detail = %+v", one)
	}
	edges := one["edges"].([]any)
	if len(edges) != 1 || edges[0].(map[string]any)["mode"] != "advisor" {
		t.Fatalf("node edges = %+v", edges)
	}
}

func TestGraphReadCarriesCharterAndKeepsConciseRowsShort(t *testing.T) {
	f := &roFakeDaemon{graph: protocol.PublicGraphPage{
		Nodes: []protocol.PublicGraphNode{{Name: "shop", Kind: "component", Maturity: "mature", Charter: &protocol.Charter{
			Purpose: "Sells things", Accepts: "bug fixes; new pages", Refuses: "payment changes", FeatureRequests: "ask the owner",
		}}},
	}}
	cs := connect(t, f.call)
	one := roCall(t, cs, "agent_graph_read", map[string]any{"node": "shop"})
	nodes := one["nodes"].([]any)
	charter, ok := nodes[0].(map[string]any)["charter"].(map[string]any)
	if !ok || charter["purpose"] != "Sells things" || len(charter["accepts"].([]any)) != 2 || charter["refuses"].([]any)[0] != "payment changes" {
		t.Fatalf("graph charter: %+v", nodes[0])
	}
	listFake := &roFakeDaemon{graph: protocol.PublicGraphPage{
		Nodes: []protocol.PublicGraphNode{{Name: "shop", Kind: "component", Maturity: "mature"}},
	}}
	listCS := connect(t, listFake.call)
	concise := roCall(t, listCS, "agent_graph_read", map[string]any{})
	conciseNode := concise["nodes"].([]any)[0].(map[string]any)
	if _, exists := conciseNode["charter"]; exists {
		t.Fatalf("concise graph node has charter: %+v", conciseNode)
	}
}

// MCP servers default to the ones this caller may use, with their tool names.
func TestMCPServersFindUsableOnly(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)

	out := roCall(t, cs, "agent_mcp_servers_find", map[string]any{})
	items := roPageItems(t, out)
	if len(items) != 1 {
		t.Fatalf("usable_only default returned %d servers, want 1: %+v", len(items), items)
	}
	row := items[0]
	if row["name"] != "files" || row["usable_by_you"] != true || row["machine"] != "nas" {
		t.Fatalf("server row = %+v", row)
	}
	if tools := row["tools"].([]any); len(tools) != 2 || tools[0] != "read" {
		t.Fatalf("tools = %+v", tools)
	}

	all := roCall(t, cs, "agent_mcp_servers_find", map[string]any{"usable_only": false})
	if got := len(roPageItems(t, all)); got != 2 {
		t.Fatalf("usable_only false returned %d servers, want 2", got)
	}
}

// Worker profiles page and list the machines that can run each one.
func TestWorkerProfilesFind(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)

	out := roCall(t, cs, "agent_worker_profiles_find", map[string]any{})
	items := roPageItems(t, out)
	if len(items) != 2 {
		t.Fatalf("got %d profiles, want 2", len(items))
	}
	if items[0]["name"] != "alpha" || items[0]["effort"] != "high" {
		t.Fatalf("profile row = %+v", items[0])
	}
	on := items[0]["available_on"].([]any)
	if len(on) != 1 || on[0] != "build" {
		t.Fatalf("available_on = %+v", on)
	}
}

// A limit outside 1..100 is refused with the valid range.
func TestReadToolsRefuseABadLimit(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)
	for _, name := range []string{"agent_messages_find", "agent_workers_find", "agent_graph_read", "agent_worker_profiles_find"} {
		res, err := cs.CallTool(t.Context(), roToolCall(name, map[string]any{"limit": 101}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !res.IsError {
			t.Errorf("%s accepted limit 101", name)
		}
	}
}

func TestGraphReadTeamActiveAndTypedEndpoints(t *testing.T) {
	active := false
	ref := protocol.GraphRef{Kind: "team", ID: "alpha"}
	owner := protocol.GraphRef{Kind: "agent", ID: "reviewer"}
	f := &roFakeDaemon{graph: protocol.PublicGraphPage{Nodes: []protocol.PublicGraphNode{{Ref: &ref, Owner: &owner, Name: "Alpha", Kind: "team", Active: &active, Charter: &protocol.Charter{Purpose: "Shared work"}}}, Edges: []protocol.PublicGraphEdge{{From: "demo", To: "alpha", FromRef: &protocol.GraphRef{Kind: "repo", ID: "demo"}, ToRef: &ref, Kind: "member_of"}}}}
	cs := connect(t, f.call)
	out := roCall(t, cs, "agent_graph_read", map[string]any{"ref": map[string]any{"kind": "team", "id": "alpha"}})
	nodes := out["nodes"].([]any)
	n := nodes[0].(map[string]any)
	if n["active"] != false || n["purpose"] != "Shared work" || n["owner"].(map[string]any)["kind"] != "agent" {
		t.Fatalf("team read: %+v", out)
	}
}
