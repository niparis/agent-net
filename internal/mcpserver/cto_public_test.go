package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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

func TestCTOSessionNeverOfferedOrdinaryAgentTools(t *testing.T) {
	ctx := context.Background()
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
	for _, tool := range cto.Tools {
		if names[tool.Name] {
			t.Errorf("ordinary tool %s offered to CTO", tool.Name)
		}
	}
	if len(cto.Tools) == 0 {
		t.Fatal("CTO has no tools")
	}
}
