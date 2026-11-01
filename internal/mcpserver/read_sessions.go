package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// The session, machine and project read tools of the tool redesign. Their
// names, descriptions and schemas come from internal/toolcatalog, one entry
// for every server. The daemon does the filtering and the paging.

func addReadSessionTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	addCatalogReadTool(s, run, "agent_sessions_find", "sessions_find")
	addCatalogReadTool(s, run, "agent_session_get", "session_get")
	addCatalogReadTool(s, run, "agent_machines_find", "machines_find")
	addCatalogReadTool(s, run, "agent_machine_get", "machine_get")
	addCatalogReadTool(s, run, "agent_projects_find", "projects_find")
}

// addCatalogReadTool registers one catalog tool. The catalog schemas are the
// advertised input and output schemas, so the SDK validates both. A successful
// result carries structuredContent and the same JSON as one text block.
func addCatalogReadTool(s *mcp.Server, run func(context.Context, string, any) (any, error), name, method string) {
	entry, ok := catalogEntry(name)
	if !ok {
		panic("mcpserver: tool " + name + " is not in the catalog")
	}
	tool := &mcp.Tool{
		Name:         entry.Name,
		Description:  entry.Description,
		InputSchema:  entry.InputSchema,
		OutputSchema: entry.OutputSchema,
	}
	if entry.AlwaysLoad {
		tool.Meta = mcp.Meta{"anthropic/alwaysLoad": true}
	}
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		out, err := run(ctx, method, in)
		if err != nil {
			return nil, nil, err
		}
		result, ok := out.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: the daemon returned %T, want a JSON object", name, out)
		}
		return nil, result, nil
	})
}

func catalogEntry(name string) (toolcatalog.Tool, bool) {
	for _, tool := range toolcatalog.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return toolcatalog.Tool{}, false
}
