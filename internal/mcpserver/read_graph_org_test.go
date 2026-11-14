package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// agent_graph_read keeps each Agent's place in the org tree through the
// output schema, and passes root to the daemon read.
func TestGraphReadKeepsTheOrgTree(t *testing.T) {
	f := &roFakeDaemon{}
	f.graph.Nodes = []protocol.PublicGraphNode{{
		Name: "member", Kind: "agent", Ref: &protocol.GraphRef{Kind: "agent", ID: "member"}, ReportsTo: "lead",
		LeadSession: &protocol.GraphSession{ID: "s_1", Name: "main", Machine: "h1", Status: "idle", Online: true, StartedBy: protocol.StartedByOwner},
		Sessions:    []protocol.GraphSession{{ID: "s_2", Name: "two", Machine: "h1", Status: "busy", Online: true, StartedBy: protocol.StartedByAgentNet}},
		Scopes:      []protocol.Scope{{Node: "member", Kind: protocol.ScopeMerge, Over: "repo:x", Note: "why", SetBy: "owner", SetAt: 1}},
	}}
	f.graph.Total = 1
	out := roCall(t, connect(t, f.call), "agent_graph_read", map[string]any{"root": "lead"})
	if len(f.graphRoots) == 0 || f.graphRoots[0] != "lead" {
		t.Fatalf("daemon read roots %v, want lead", f.graphRoots)
	}
	node := out["nodes"].([]any)[0].(map[string]any)
	got := map[string]any{}
	for _, key := range []string{"reports_to", "lead_session", "sessions", "scopes"} {
		got[key] = node[key]
	}
	raw, _ := json.Marshal(got)
	want := `{"lead_session":{"id":"s_1","name":"main","online":true,"started_by":"owner"},"reports_to":"lead","scopes":[{"kind":"merge","note":"why","over":"repo:x"}],"sessions":[{"id":"s_2","name":"two","online":true,"started_by":"agent-net"}]}`
	if string(raw) != want {
		t.Fatalf("org tree in agent_graph_read:\n got %s\nwant %s", raw, want)
	}
}

// agent_session_get keeps the two org lines through the output schema.
func TestSessionGetKeepsTheOrgLines(t *testing.T) {
	cs := connect(t, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		return json.RawMessage(`{"session":{"id":"s_1","name":"s01","kind":"agent","machine":"research","project":"housing","harness":"claude","model":"standard","online":true,"status":"idle","task":"","last_seen":"2026-01-01T12:00:00Z","task_started":null,"reports_to":"lead (lead session main)","also_answers_to":"chief (asks)"}}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_session_get", Arguments: map[string]any{"session": "s_1"}})
	if err != nil || res.IsError {
		t.Fatalf("%+v %v", res, err)
	}
	session := res.StructuredContent.(map[string]any)["session"].(map[string]any)
	if session["reports_to"] != "lead (lead session main)" || session["also_answers_to"] != "chief (asks)" {
		t.Fatalf("org lines lost: %+v", session)
	}
}
