package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentFileSendForwardsAndValidates(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		calls = append(calls, call{method, p})
		return json.RawMessage(`{"message_id":"m_1","files":[{"name":"a.txt","path":"inbox/s_b/m_1/a.txt"}]}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{"/tmp/a.txt"}, "note": "read"}})
	if err != nil || res.IsError {
		t.Fatalf("file send: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "file_send" || calls[0].params["to"] != "s_b" || calls[0].params["note"] != "read" {
		t.Fatalf("calls: %+v", calls)
	}
	validateAgainstCatalog(t, "agent_file_send", res.StructuredContent)
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{}, "note": "read"}})
	if err != nil || !bad.IsError || len(calls) != 1 {
		t.Fatalf("bad args: %v %+v calls %v", err, bad, calls)
	}
}

func TestAgentFileSendDaemonError(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return nil, errors.New("workers send files with agent_handback")
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_file_send", Arguments: map[string]any{"to": "s_b", "paths": []string{"/tmp/a"}, "note": "x"}})
	if err != nil || !res.IsError {
		t.Fatalf("daemon error: %v %+v", err, res)
	}
}
