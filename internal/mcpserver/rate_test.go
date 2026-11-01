package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentRateShowsTheResult(t *testing.T) {
	accepted := true
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method != "agent_rate" {
			return json.RawMessage(`{}`), nil
		}
		if accepted {
			return json.RawMessage(`{"accepted":true,"worker":"s_w1","score":4,"model":"deepseek-v4.1-flash","difficulty":"normal"}`), nil
		}
		return json.RawMessage(`{"accepted":false,"reason":"this worker already has a rating"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_rate",
		Arguments: map[string]any{"worker": "s_w1", "score": 4, "note": "good"}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("agent_rate: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Recorded rating 4") ||
		!strings.Contains(text, "s_w1") || !strings.Contains(text, "deepseek-v4.1-flash") {
		t.Errorf("rate text: %q", text)
	}
	accepted = false
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_rate",
		Arguments: map[string]any{"worker": "s_w1", "score": 2}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("refused agent_rate: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Not recorded: this worker already has a rating") {
		t.Errorf("refusal text: %q", text)
	}
}
