package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentMessageHandoffForwardsAndValidates(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		return json.RawMessage(`{"message_id":"m_2","to":{"id":"s_b","name":"bob"},"state":"stored"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_handoff", Arguments: map[string]any{"message_id": "m_1", "to_session": "bob", "note": "yours"}})
	if err != nil || res.IsError {
		t.Fatalf("handoff: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "handoff" || calls[0].params["message_id"] != "m_1" || calls[0].params["to_session"] != "bob" || calls[0].params["note"] != "yours" {
		t.Fatalf("calls: %+v", calls)
	}
	if key, _ := calls[0].params["idempotency_key"].(string); key == "" {
		t.Fatalf("no idempotency key: %+v", calls[0].params)
	}
	validateAgainstCatalog(t, "agent_message_handoff", res.StructuredContent)
	// The note is optional; the message and the session are not.
	if res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_handoff", Arguments: map[string]any{"message_id": "m_1", "to_session": "bob"}}); err != nil || res.IsError {
		t.Fatalf("no note: %v %+v", err, res)
	}
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_handoff", Arguments: map[string]any{"message_id": "m_1"}})
	if err != nil || !bad.IsError || len(calls) != 2 {
		t.Fatalf("bad args: %v %+v calls %v", err, bad, calls)
	}
}
