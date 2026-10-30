package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/client"
)

// NewCTO is deliberately separate from the network MCP registry. This list is
// the single place to add a CTO tool and its argument schema.
func NewCTO(call Caller, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "agent-net-cto", Version: version}, nil)
	fields := map[string]map[string]string{
		"graph_get": {}, "charter_set": {"node": "string", "attr": "string", "value": "string"}, "charter_history": {"node": "string", "limit": "integer"},
		"node_add": {"name": "string"}, "node_remove": {"name": "string"}, "edge_set": {"from": "string", "to": "string", "kind": "string", "mode": "string", "rule": "string"},
		"edge_remove": {"from": "string", "to": "string", "kind": "string"}, "edge_history": {"node": "string", "limit": "integer"}, "graph_undo": {"id": "integer"},
		"agents": {"text": "string", "project": "string"}, "workers": {}, "needs_you": {}, "feed": {"limit": "integer"},
		"send": {"to": "string", "body": "string"}, "worker_start": {"project": "string", "harness": "string", "model": "string", "task": "string", "machine": "string", "isolation": "string"},
	}
	for name, fs := range fields {
		props := map[string]any{}
		for k, v := range fs {
			props[k] = map[string]string{"type": v}
		}
		tool := &mcp.Tool{Name: name, Description: "CTO: " + name, InputSchema: map[string]any{"type": "object", "properties": props, "additionalProperties": false}}
		method := name
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw, err := call(ctx, method, req.Params.Arguments)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
		})
	}
	return s
}

func RunCTO(ctx context.Context, socket, version string) error {
	c, err := client.Dial(socket)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = c.Call(ctx, "hello", map[string]string{"client": "cto-mcp"}); err != nil {
		return fmt.Errorf("CTO channel: %w", err)
	}
	s := NewCTO(func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		return c.Call(ctx, "cto_tool", map[string]any{"name": method, "arguments": params})
	}, version)
	return s.Run(ctx, &mcp.StdioTransport{})
}
