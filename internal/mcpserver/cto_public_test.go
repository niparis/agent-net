package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestCTOPublicReadTools(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	removed := strings.Fields("charter_suggestions graph_get charter_history edge_history needs_you protected_providers_get info session_text usage usage_top counts graph_view session_enter project_display_get integrations_get held_list inbox_list outcome_inbox node_settings_get playbook_get cto_enter cto_graph_history tools_list tools_history mcp_import_candidates tools_differences")
	for _, tool := range list.Tools {
		for _, name := range removed {
			if tool.Name == name {
				t.Errorf("owner read %s remains", name)
			}
		}
	}
}

func TestCTOSessionNeverOfferedAgentOnlyTools(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AGENT_NET_WORKER", "")
	ordinary, err := connect(t, func(context.Context, string, any) (json.RawMessage, error) { return nil, nil }).ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cto, err := ctoTestSession(t).ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range ordinary.Tools {
		names[tool.Name] = true
	}
	catalog := map[string]bool{}
	for _, tool := range toolcatalog.Tools {
		catalog[tool.Name] = true
	}
	for _, tool := range cto.Tools {
		if names[tool.Name] && catalog[tool.Name] && !toolcatalog.Allows(toolcatalog.RoleCTO, tool.Name) {
			t.Errorf("agent-only catalog tool %s offered to CTO", tool.Name)
		}
	}
	if len(cto.Tools) == 0 {
		t.Fatal("CTO has no tools")
	}
}

func TestSettingsReadOfferedToAgentsAndCTO(t *testing.T) {
	ctx := context.Background()
	for _, session := range []*mcp.ClientSession{connect(t, func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`[]`), nil }), ctoTestSession(t)} {
		list, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range list.Tools {
			if tool.Name == "agent_settings_read" {
				found = true
			}
		}
		if !found {
			t.Fatal("settings read missing")
		}
	}
}

func TestCTOSettingsChangeSchemaAcceptsBatchAndUndo(t *testing.T) {
	for _, raw := range []string{`{"area":"values_write","changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"reason":"test"}`, `{"area":"values_undo","revision":1}`} {
		if err := ValidateCTOActionSchema("settings_change", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		_ = json.Unmarshal([]byte(raw), &input)
		result, err := ctoTestSession(t).CallTool(context.Background(), &mcp.CallToolParams{Name: "settings_change", Arguments: input})
		if err != nil || result.IsError {
			t.Fatalf("call %s: %+v %v", raw, result, err)
		}
	}
}

func TestCTOSettingsReadKeepsLargeInteger(t *testing.T) {
	ctx := context.Background()
	a, b := mcp.NewInMemoryTransports()
	s := NewCTO(func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		return json.RawMessage(`{"settings":[{"key":"example","value":9007199254740993}]}`), nil
	}, "test")
	ss, err := s.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_settings_read", Arguments: map[string]any{"key": "example"}})
	if err != nil || result.IsError {
		t.Fatalf("result: %+v %v", result, err)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "9007199254740993") {
		t.Fatalf("precision lost: %+v", result.Content)
	}
}
