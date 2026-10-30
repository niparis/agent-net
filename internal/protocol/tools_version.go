package protocol

import "fmt"

// ToolsUnknown is the ToolsVersion of a session whose `agent-net mcp` process
// sent no version (it is older than the release that started sending it).
const ToolsUnknown = "unknown"

// ToolsStaleNote is the one line a stale `agent-net mcp` process adds, once, to
// a tool result. mine is the version of its tools and daemon is the version
// the machine runs. Codex is told to restart the thread's MCP server, Claude
// Code to reconnect with /mcp.
func ToolsStaleNote(harness, mine, daemon string) string {
	how := "in Claude Code type /mcp, pick agent-net, reconnect"
	if harness == "codex" {
		how = "in Codex restart Codex, then resume this thread"
	}
	return fmt.Sprintf("agent-net: your agent-net tools are %s (this machine runs %s). "+
		"Ask your user to reconnect agent-net: %s. New tools appear after that.", mine, daemon, how)
}
