package toolcatalog

import (
	"fmt"
	"strings"
)

// renamed lists the tool names from before the tool redesign (v0.4.71-74)
// with the catalog tools that replaced them. No server lists or runs an old
// name; a call with one gets RenamedError, so a model that remembers the old
// name learns the new one in one step.
var renamed = map[string][]string{
	// Old agent tools.
	"agent_list":             {"agent_sessions_find"},
	"agent_get":              {"agent_session_get"},
	"agent_nodes":            {"agent_machines_find", "agent_machine_get"},
	"agent_project_list":     {"agent_projects_find"},
	"agent_messages":         {"agent_messages_find"},
	"agent_message_status":   {"agent_message_get"},
	"agent_send":             {"agent_message_send"},
	"agent_reply":            {"agent_message_reply"},
	"agent_graph":            {"agent_graph_read"},
	"agent_mcp_list":         {"agent_mcp_servers_find"},
	"agent_worker_profiles":  {"agent_worker_profiles_find"},
	"agent_start":            {"agent_worker_start"},
	"agent_stop":             {"agent_worker_stop"},
	"agent_rate":             {"agent_worker_rate"},
	"agent_ask_owner":        {"agent_owner_ask"},
	"agent_request_approval": {"agent_owner_ask"},
	"agent_propose_idea":     {"agent_owner_ask"},

	// Old CTO tools.
	"overview":  {"agent_sessions_find", "agent_machines_find"},
	"sessions":  {"agent_sessions_find"},
	"session":   {"agent_session_get"},
	"proposals": {"cto_proposals_find"},
	"feed":      {"agent_messages_find"},
	"messages":  {"agent_messages_find", "agent_message_get"},
	"graph":     {"agent_graph_read"},
	"machines":  {"agent_machines_find", "agent_machine_get"},
	"projects":  {"agent_projects_find"},
	"workers":   {"agent_workers_find"},
	"tools":     {"agent_mcp_servers_find"},
	"settings":  {"agent_settings_read", "agent_worker_profiles_find"},
	"message":   {"agent_message_send", "agent_message_reply"},
	"ask_owner": {"agent_owner_ask"},
	"learn":     {"agent_learn"},
	"ui":        {"cto_ui_show"},
}

// RenamedError returns the one-line error for a call with an old tool name,
// or nil when name is not an old name.
func RenamedError(name string) error {
	repl, ok := renamed[name]
	if !ok {
		return nil
	}
	return fmt.Errorf("%s was renamed to %s.", name, strings.Join(repl, " or "))
}
