package mcpserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func wantCatalogTool(t *testing.T, name string) toolcatalog.Tool {
	t.Helper()
	for _, tool := range toolcatalog.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("catalog has no %s", name)
	return toolcatalog.Tool{}
}

func normalizeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("json %s: %v", raw, err)
	}
	return v
}

func validateAgainstCatalog(t *testing.T, name string, value any) {
	t.Helper()
	tool := wantCatalogTool(t, name)
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
		t.Fatalf("%s output schema: %v", name, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("%s output schema resolve: %v", name, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(normalizeJSON(t, raw)); err != nil {
		t.Fatalf("%s output %s does not match the catalog schema: %v", name, raw, err)
	}
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", res.Content[0])
	}
	return text.Text
}

func TestMessageToolsUseTheCatalogSchemas(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) { return nil, nil })
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		found[tool.Name] = tool
	}
	for _, name := range []string{"agent_message_send", "agent_message_reply"} {
		got := found[name]
		if got == nil {
			t.Fatalf("%s is not registered", name)
		}
		want := wantCatalogTool(t, name)
		if got.Description != want.Description {
			t.Errorf("%s description differs from the catalog", name)
		}
		for label, pair := range map[string][2]any{
			"input":  {got.InputSchema, want.InputSchema},
			"output": {got.OutputSchema, want.OutputSchema},
		} {
			gotRaw, _ := json.Marshal(pair[0])
			wantRaw, _ := json.Marshal(pair[1])
			if !reflect.DeepEqual(normalizeJSON(t, gotRaw), normalizeJSON(t, wantRaw)) {
				t.Errorf("%s %s schema differs from the catalog:\ngot  %s\nwant %s", name, label, gotRaw, wantRaw)
			}
		}
	}
}

func TestAgentMessageSendToOneSession(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		return json.RawMessage(`{"sent":[{"message_id":"m_1","to":{"id":"s_x","name":"x"},"state":"stored"}]}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send",
		Arguments: map[string]any{"to": "s_x", "body": "please review"}})
	if err != nil || res.IsError {
		t.Fatalf("send: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "send" || calls[0].params["to"] != "s_x" ||
		calls[0].params["body"] != "please review" || calls[0].params["type"] != "request" ||
		calls[0].params["receipt"] != true || calls[0].params["idempotency_key"] == "" {
		t.Fatalf("daemon call: %+v", calls)
	}
	validateAgainstCatalog(t, "agent_message_send", res.StructuredContent)
	text := resultText(t, res)
	if !reflect.DeepEqual(normalizeJSON(t, []byte(text)), res.StructuredContent) {
		t.Fatalf("text %s is not the structured content", text)
	}
}

func TestAgentMessageSendGroupDefaults(t *testing.T) {
	var got map[string]any
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		json.Unmarshal(raw, &got)
		return json.RawMessage(`{"sent":[{"message_id":"m_1","to":{"id":"s_a","name":"a"},"state":"stored"},{"message_id":"m_2","to":{"id":"s_b","name":"b"},"state":"stored"}]}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send",
		Arguments: map[string]any{"to_set": map[string]any{"project": "shop"}, "body": "all hands", "type": "notification"}})
	if err != nil || res.IsError {
		t.Fatalf("group send: %v %+v", err, res)
	}
	set, _ := got["to_set"].(map[string]any)
	if set == nil || set["kind"] == nil || set["online_only"] != true {
		t.Fatalf("group defaults: %+v", got)
	}
	if got["type"] != "notification" {
		t.Fatalf("type: %+v", got)
	}
	validateAgainstCatalog(t, "agent_message_send", res.StructuredContent)
}

func TestAgentMessageSendRefusesBadSelectors(t *testing.T) {
	calls := 0
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"sent":[]}`), nil
	})
	for _, args := range []map[string]any{
		{"body": "neither"},
		{"to": "s_x", "to_set": map[string]any{"project": "shop"}, "body": "both"},
		{"to_set": map[string]any{}, "body": "no filter"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send", Arguments: args})
		if err != nil || !res.IsError {
			t.Fatalf("send %v: %v %+v", args, err, res)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid calls reached the daemon: %d", calls)
	}
}

func TestAgentMessageReplyOutcomeRules(t *testing.T) {
	var calls []call
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, _ := json.Marshal(params)
		var m map[string]any
		json.Unmarshal(raw, &m)
		calls = append(calls, call{method, m})
		return json.RawMessage(`{"message_id":"m_1","to":{"id":"s_x","name":"x"},"state":"stored"}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply",
		Arguments: map[string]any{"message": "m_1", "body": "done", "outcome": "accept"}})
	if err != nil || res.IsError {
		t.Fatalf("reply: %v %+v", err, res)
	}
	if len(calls) != 1 || calls[0].method != "reply" || calls[0].params["message_id"] != "m_1" ||
		calls[0].params["outcome"] != "accept" || calls[0].params["receipt"] != true {
		t.Fatalf("reply call: %+v", calls)
	}
	validateAgainstCatalog(t, "agent_message_reply", res.StructuredContent)
	text := resultText(t, res)
	if !reflect.DeepEqual(normalizeJSON(t, []byte(text)), res.StructuredContent) {
		t.Fatalf("text %s is not the structured content", text)
	}

	for _, args := range []map[string]any{
		{"message": "m_1", "body": "no", "outcome": "reject"},
		{"message": "m_1", "body": "elsewhere", "outcome": "redirect", "reason": "not me"},
		{"message": "m_1", "body": "ask", "outcome": "escalate", "reason": "unclear"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_reply", Arguments: args})
		if err != nil || !res.IsError {
			t.Fatalf("reply %v: %v %+v", args, err, res)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("invalid outcomes reached the daemon: %+v", calls)
	}
}

func TestAgentMessageSendDaemonErrorIsAToolError(t *testing.T) {
	cs := connect(t, func(context.Context, string, any) (json.RawMessage, error) {
		return nil, errGroupOnly
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_send",
		Arguments: map[string]any{"to": "s_x", "body": "hi"}})
	if err != nil || !res.IsError {
		t.Fatalf("daemon refusal: %v %+v", err, res)
	}
	if !strings.Contains(resultText(t, res), "group messages are for the CTO") {
		t.Fatalf("error text: %s", resultText(t, res))
	}
}

var errGroupOnly = &staticError{"group messages are for the CTO and advisor nodes only; send to one session with to"}

type staticError struct{ text string }

func (e *staticError) Error() string { return e.text }
