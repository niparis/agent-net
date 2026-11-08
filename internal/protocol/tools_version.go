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

// ToolsDisconnected is the ToolsVersion of a live Claude Code session that has
// had no `agent-net mcp` connection to the daemon for longer than the grace
// period, for example because Claude Code stopped the process and never
// restarted it. The session still gets turn text through its hooks, but it has
// no agent_* tools.
const ToolsDisconnected = "disconnected"

// ToolsDisconnectedDetail is the owner's detail line for such a session.
const ToolsDisconnectedDetail = "agent-net tools disconnected; the CTO can restart it (session_change restart), or type /mcp to reconnect"

// ToolsDisconnectedNotice is the line the turn text of such a session carries.
const ToolsDisconnectedNotice = "[agent-net] Your agent-net tools are disconnected. Tell the CTO or agent-net; the CTO can restart this session (session_change restart). Until then you cannot message other agents."
