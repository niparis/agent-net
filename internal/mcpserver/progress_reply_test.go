package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A progress reply passes follow_up_at to the daemon. The catalog schema must
// allow the field, or the call is refused before it reaches the daemon.
func TestAgentMessageReplyPassesProgressFollowUpAt(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		return json.RawMessage(`{"message_id":"m_2","to":{"id":"s_x","name":"x"},"state":"delivered"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply",
		Arguments: map[string]any{"message": "m_1", "body": "About two hours more.", "progress": true, "follow_up_at": "2h"}})
	if err != nil || res.IsError {
		t.Fatalf("progress reply with follow_up_at: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "reply" || calls[0].params["progress"] != true || calls[0].params["follow_up_at"] != "2h" {
		t.Fatalf("reply call: %+v", calls)
	}
	validateAgainstCatalog(t, "agent_message_reply", res.StructuredContent)
}
