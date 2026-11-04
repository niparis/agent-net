package mcpserver

// Role filtering for the catalog tools: the catalog tools a server lists are
// exactly those of its caller's role. A call with a tool name from before the
// redesign gets a one-line error that names the new tool.

import (
	"context"
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
// outside the catalog are not touched.
func filterCatalogTools(s *mcp.Server, role toolcatalog.Role) {
	for _, tool := range toolcatalog.Tools {
		if !toolcatalog.Allows(role, tool.Name) {
			s.RemoveTools(tool.Name)
		}
	}
}

// renamedTools is a receiving middleware. A tools/call with an old tool name
// returns a tool error that names the replacement, instead of the SDK's
// unknown-tool protocol error. The call never reaches the daemon.
func renamedTools() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
					if err := toolcatalog.RenamedError(call.Params.Name); err != nil {
						return errResult(err.Error()), nil
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}
