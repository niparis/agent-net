package toolcatalog

// daemonFields lists the catalog input fields that a tool sends to its daemon
// method under another name. A field not listed goes to the daemon under its
// own name. The MCP server reads this table, and the native plugins (Pi and
// OpenCode) get it in the generated catalog, so each rename is written once.
// The MCP test in internal/mcpserver fails when the MCP server and this table
// disagree.
var daemonFields = map[string]map[string]string{
	"agent_message_get":      {"message": "message_id"},
	"agent_message_reply":    {"message": "message_id"},
	"agent_message_withdraw": {"message": "message_id"},
	"agent_messages_find":    {"conversation": "conversation_id"},
	"agent_mcp_test":         {"server": "name"},
	"agent_worker_stop":      {"worker": "session"},
}

// DaemonField returns the name the daemon method gets for one catalog input
// field of a tool.
func DaemonField(tool, field string) string {
	if name, ok := daemonFields[tool][field]; ok {
		return name
	}
	return field
}

// DaemonFields returns the renamed fields of a tool, from the catalog name to
// the daemon name. It is never nil.
func DaemonFields(tool string) map[string]string {
	out := map[string]string{}
	for field, name := range daemonFields[tool] {
		out[field] = name
	}
	return out
}
