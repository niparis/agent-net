package mcpserver

// Role filtering for the catalog tools. Old tool names are left alone; the
// catalog tools a server lists are exactly those of its caller's role.

import (
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// callerRole is the role this server process serves. A worker's harness process
// carries AGENT_NET_WORKER; every other agent server is an ordinary agent.
func callerRole() toolcatalog.Role {
	if os.Getenv("AGENT_NET_WORKER") != "" {
		return toolcatalog.RoleWorker
	}
	return toolcatalog.RoleAgent
}

// filterCatalogTools removes every catalog tool the role may not use. Tools
// outside the catalog (the old names) are not touched.
func filterCatalogTools(s *mcp.Server, role toolcatalog.Role) {
	for _, tool := range toolcatalog.Tools {
		if !toolcatalog.Allows(role, tool.Name) {
			s.RemoveTools(tool.Name)
		}
	}
}
