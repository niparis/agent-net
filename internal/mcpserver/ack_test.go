package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A reply with ack closes a request with no body, and the daemon receives the
// flag.
func TestAgentMessageReplyAck(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		return json.RawMessage(`{"message_id":"m_1","to":{"id":"s_x","name":"x"},"state":"acknowledged"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply",
		Arguments: map[string]any{"message": "m_1", "ack": true}})
	if err != nil || res.IsError {
		t.Fatalf("ack: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "reply" || calls[0].params["ack"] != true ||
		calls[0].params["body"] != "" || calls[0].params["outcome"] != "" {
		t.Fatalf("ack call: %+v", calls)
	}
	validateAgainstCatalog(t, "agent_message_reply", res.StructuredContent)

	// ack cannot be combined with outcome.
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply",
		Arguments: map[string]any{"message": "m_1", "ack": true, "outcome": "accept"}})
	if err != nil || !res.IsError {
		t.Fatalf("ack with outcome: %v %+v", err, res)
	}
	if !strings.Contains(resultText(t, res), "ack") {
		t.Fatalf("error text: %s", resultText(t, res))
	}
	if len(calls) != 1 {
		t.Fatalf("refused ack reached the daemon: %+v", calls)
	}

	// An empty body without ack is still refused.
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply",
		Arguments: map[string]any{"message": "m_1"}})
	if err != nil || !res.IsError {
		t.Fatalf("empty reply: %v %+v", err, res)
	}
	if len(calls) != 1 {
		t.Fatalf("empty reply reached the daemon: %+v", calls)
	}
}
