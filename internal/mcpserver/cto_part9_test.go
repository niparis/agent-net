package mcpserver

import (
	"context"
	"testing"
)

// The CTO reads through the catalog tools that replaced the part 9 reads
// (overview, sessions, session, proposals, feed, messages, graph, machines,
// projects, workers, tools, settings).
func TestCTOPart9ReadTools(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tool := range list.Tools {
		have[tool.Name] = true
	}
	for _, name := range []string{"agent_sessions_find", "agent_session_get", "cto_proposals_find", "agent_messages_find", "agent_message_get", "agent_graph_read", "agent_machines_find", "agent_machine_get", "agent_machine_log", "agent_projects_find", "agent_workers_find", "agent_mcp_servers_find", "agent_settings_read", "agent_worker_profiles_find"} {
		if !have[name] {
			t.Errorf("missing read tool %s", name)
		}
	}
}
