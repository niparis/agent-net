package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOLeadSetCatalogSchema(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "graph_change" {
			b, _ := json.Marshal(tool.InputSchema)
			found = strings.Contains(string(b), `"lead_set"`) && strings.Contains(string(b), `"lead"`)
		}
	}
	if !found {
		t.Fatal("advertised graph_change schema has no lead_set action and lead field")
	}
	for _, kind := range []string{"repo", "team"} {
		res, err := ctoTestSession(t).CallTool(context.Background(), &mcp.CallToolParams{Name: "graph_change", Arguments: map[string]any{"action": "lead_set", "ref": map[string]any{"kind": kind, "id": "x"}, "lead": map[string]any{"kind": "agent", "id": "a"}}})
		if err != nil || res.IsError {
			t.Fatalf("%s: %+v %v", kind, res, err)
		}
	}
}

// agent_graph_read is the tool the CTO reads, so an Agent's lead level must
// reach its node there.
func TestGraphReadShowsAgentLeadLevel(t *testing.T) {
	nodes := roGraphNodes([]protocol.PublicGraphNode{{Name: "lead-agent", Kind: "agent", LeadLevel: protocol.LeadLevelTeamLead}})
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"lead_level":"team lead"`) {
		t.Fatalf("lead level lost in agent_graph_read: %s", raw)
	}
}
