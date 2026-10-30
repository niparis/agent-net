package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStartShowsPermissions(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"harness":"codex","permissions":"no-network","status":"queued"}`), nil
	})
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_start", Arguments: map[string]any{"task": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, c := range result.Content {
		if v, ok := c.(*mcp.TextContent); ok {
			text += v.Text
		}
	}
	if !strings.Contains(text, "permissions: no-network (writes only in its worktree, network off)") {
		t.Fatalf("%s", text)
	}
}
