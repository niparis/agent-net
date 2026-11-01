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

// agent_rate takes kind defect_found_later: no score, one note, and the
// answer says a defect was recorded.
func TestAgentRateDefectFoundLater(t *testing.T) {
	var saw map[string]any
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method != "agent_rate" {
			return json.RawMessage(`{}`), nil
		}
		raw, _ := json.Marshal(params)
		json.Unmarshal(raw, &saw)
		return json.RawMessage(`{"accepted":true,"worker":"s_w1","kind":"defect_found_later","model":"deepseek-v4.1-flash","difficulty":"hard"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_rate",
		Arguments: map[string]any{"worker": "s_w1", "kind": "defect_found_later", "note": "old path left in place"}})
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("agent_rate defect: %v %+v", err, res)
	}
	if saw["kind"] != "defect_found_later" || saw["note"] != "old path left in place" {
		t.Fatalf("defect request: %+v", saw)
	}
	if _, ok := saw["score"]; ok {
		t.Fatalf("a defect report sent a score: %+v", saw)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Recorded defect found later") ||
		!strings.Contains(text, "s_w1") || !strings.Contains(text, "deepseek-v4.1-flash") {
		t.Errorf("defect text: %q", text)
	}
}
