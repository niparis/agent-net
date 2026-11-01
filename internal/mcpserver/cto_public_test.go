package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestCTOPublicReadTools(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	removed := strings.Fields("charter_suggestions graph_get charter_history edge_history workers needs_you feed protected_providers_get info session_text usage usage_top counts graph_view session_enter project_display_get integrations_get held_list inbox_list outcome_inbox node_settings_get playbook_get cto_enter cto_graph_history tools_list tools_history mcp_import_candidates tools_differences")
	for _, tool := range list.Tools {
		for _, name := range removed {
			if tool.Name == name {
				t.Errorf("owner read %s remains", name)
			}
		}
	}
}
