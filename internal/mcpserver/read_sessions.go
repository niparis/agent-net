package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

// readToolMethods names the daemon method of each read tool in one place. The
// new tool and its old-name alias both call readToolCall, so an alias adds no
// second path.
var readToolMethods = map[string]string{
	"agent_sessions_find": "sessions_find",
	"agent_session_get":   "session_get",
	"agent_machines_find": "machines_find",
	"agent_machine_get":   "machine_get",
	"agent_projects_find": "projects_find",
}

// readToolCall runs the daemon call behind one read tool.
func readToolCall(ctx context.Context, run func(context.Context, string, any) (any, error), tool string, in any) (any, error) {
	method, ok := readToolMethods[tool]
	if !ok {
		panic("mcpserver: no read method for " + tool)
	}
	return run(ctx, method, in)
}

// addReadSessionTools registers the session, machine and project read tools of
// the tool redesign. Their names, descriptions and schemas come from
// internal/toolcatalog. The daemon does the filtering and the paging.
func addReadSessionTools(s *mcp.Server, run func(context.Context, string, any) (any, error)) {
	for _, name := range []string{"agent_sessions_find", "agent_session_get", "agent_machines_find", "agent_machine_get", "agent_projects_find"} {
		addCatalogReadTool(s, run, name)
	}
}

// addCatalogReadTool registers one catalog tool. The catalog input schema is
// advertised and its defaults are applied. The output schema is not advertised;
// each successful result is validated against it and carries structuredContent
// and the same JSON as one text block.
func addCatalogReadTool(s *mcp.Server, run func(context.Context, string, any) (any, error), name string) {
	entry, ok := catalogEntry(name)
	if !ok {
		panic("mcpserver: tool " + name + " is not in the catalog")
	}
	tool := &mcp.Tool{
		Name:        entry.Name,
		Description: entry.Description,
		InputSchema: entry.InputSchema,
	}
	if entry.AlwaysLoad {
		tool.Meta = mcp.Meta{"anthropic/alwaysLoad": true}
	}
	s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := catalogInput(name, req)
		if err != nil {
			return toolError(err)
		}
		var in map[string]any
		if err := json.Unmarshal(raw, &in); err != nil {
			return toolError(err)
		}
		out, err := readToolCall(ctx, run, name, in)
		if err != nil {
			return toolError(err)
		}
		result, ok := out.(map[string]any)
		if !ok {
			return toolError(fmt.Errorf("%s: the daemon returned %T, want a JSON object", name, out))
		}
		return catalogResult(name, result)
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
