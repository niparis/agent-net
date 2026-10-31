package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/niparis/agent-net/internal/protocol"
)

func lastText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("no content: %+v", res)
	}
	tc, ok := res.Content[len(res.Content)-1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("last content is %T", res.Content[len(res.Content)-1])
	}
	return tc.Text
}

func hasReminder(res *mcp.CallToolResult) bool {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text == AnnounceReminder {
			return true
		}
	}
	return false
}

// Until the session announces, every agent-net tool result ends with the
// reminder line - errors too. After a successful announce it is gone.
func TestReminderUntilAnnounce(t *testing.T) {
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "send" {
			return nil, errors.New("gateway says no")
		}
		if method == "announce" {
			raw, _ := json.Marshal(params)
			var m map[string]any
			json.Unmarshal(raw, &m)
			if m["task"] == "bad" {
				return nil, errors.New("rejected")
			}
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	ctx := context.Background()
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"agent_list", nil},
		{"agent_get", map[string]any{"session_id": "s_1"}},
		{"agent_nodes", nil},
		{"agent_send", map[string]any{"to": "s_x", "body": "hi"}}, // an error result
	} {
		res := call(c.tool, c.args)
		if got := lastText(t, res); got != AnnounceReminder {
			t.Errorf("%s before announce: last line %q, want the reminder", c.tool, got)
		}
	}
	if res := call("agent_send", map[string]any{"to": "s_x", "body": "hi"}); !res.IsError {
		t.Fatalf("the send error should stay an error: %+v", res)
	}

	// A failed announce does not count.
	if res := call("agent_announce", map[string]any{"task": "bad"}); !res.IsError || !hasReminder(res) {
		t.Fatalf("failed announce: reminder %v, error %v", hasReminder(res), res.IsError)
	}
	// The announce that works has no reminder, and neither does anything after it.
	if res := call("agent_announce", map[string]any{"task": "fixing the login bug"}); res.IsError || hasReminder(res) {
		t.Fatalf("good announce: %+v", res)
	}
	for _, tool := range []string{"agent_list", "agent_nodes", "agent_messages"} {
		if res := call(tool, nil); hasReminder(res) {
			t.Errorf("%s after announce still has the reminder", tool)
		}
	}
	if res := call("agent_send", map[string]any{"to": "s_x", "body": "hi"}); hasReminder(res) {
		t.Error("an error result after announce still has the reminder")
	}
}

// Each server has its own state: a new session starts without having announced.
func TestReminderIsPerServer(t *testing.T) {
	fn := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	a, b := connect(t, fn), connect(t, fn)
	ctx := context.Background()
	if _, err := a.CallTool(ctx, &mcp.CallToolParams{Name: "agent_announce", Arguments: map[string]any{"task": "x"}}); err != nil {
		t.Fatal(err)
	}
	res, err := b.CallTool(ctx, &mcp.CallToolParams{Name: "agent_list"})
	if err != nil || !hasReminder(res) {
		t.Fatalf("second server: %v %+v", err, res)
	}
}

// The waiting question is an optional announce parameter (decision 0016).
// The daemon reads the short string form as an input question.
func TestAnnouncePassesTheWaitingQuestion(t *testing.T) {
	var got protocol.ProfileAnnounce
	cs := connect(t, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		if method == "announce" {
			raw, _ := json.Marshal(params)
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Errorf("params: %v", err)
			}
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_announce", Arguments: map[string]any{
		"task": "deploy", "waiting": "Merge tenebrae-nexus/truenas-stacks #817?"}})
	if err != nil || res.IsError {
		t.Fatalf("announce: %v %+v", err, res)
	}
	if got.Waiting == nil || got.Waiting.Kind != protocol.WaitingInput || got.Waiting.Text != "Merge tenebrae-nexus/truenas-stacks #817?" {
		t.Fatalf("waiting not passed: %+v", got.Waiting)
	}
}

func TestAnnounceRuleInInstructionsAndToolDescription(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := New(func(context.Context, string, any) (json.RawMessage, error) { return nil, nil }, "").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ins := cs.InitializeResult().Instructions
	for _, want := range []string{"agent_announce", "start of every session", "before other work", "whenever your task changes", "AGENTS.md"} {
		if !strings.Contains(ins, want) {
			t.Errorf("server instructions miss %q: %s", want, ins)
		}
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "agent_announce" {
			continue
		}
		for _, want := range []string{"REQUIRED", "start of every session", "whenever your task changes", "AGENTS.md"} {
			if !strings.Contains(tl.Description, want) {
				t.Errorf("agent_announce description misses %q: %s", want, tl.Description)
			}
		}
		b, _ := json.Marshal(tl.InputSchema)
		var schema struct {
			Required []string `json:"required"`
		}
		json.Unmarshal(b, &schema)
		if len(schema.Required) != 1 || schema.Required[0] != "task" {
			t.Errorf("only task is required, schema says %v", schema.Required)
		}
		return
	}
	t.Fatal("no agent_announce tool")
}

// A session cannot rename itself (decision 0032): agent_announce has no name
// parameter and its description does not mention one.
func TestAnnounceHasNoNameParameter(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := New(func(context.Context, string, any) (json.RawMessage, error) { return nil, nil }, "").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "agent_announce" {
			continue
		}
		b, _ := json.Marshal(tl.InputSchema)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if json.Unmarshal(b, &schema) != nil {
			t.Fatalf("schema: %s", b)
		}
		if _, ok := schema.Properties["name"]; ok {
			t.Errorf("agent_announce still takes a name: %s", b)
		}
		for _, word := range []string{"rename", "name unless"} {
			if strings.Contains(tl.Description, word) {
				t.Errorf("agent_announce description still mentions %q: %s", word, tl.Description)
			}
		}
		return
	}
	t.Fatal("no agent_announce tool")
}

func TestAskOwnerPassesQuestion(t *testing.T) {
	var got protocol.WaitingRequest
	cs := connect(t, func(_ context.Context, method string, params any) (json.RawMessage, error) {
		if method == protocol.MethodWaiting {
			b, _ := json.Marshal(params)
			_ = json.Unmarshal(b, &got)
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_ask_owner", Arguments: map[string]any{"question": "Merge?", "options": []string{"Yes", "No"}, "urgency": "high"}})
	if err != nil || res.IsError || got.Kind != protocol.WaitingQuestion || got.Text != "Merge?" || len(got.Options) != 2 || got.Urgency != "high" || !got.Explicit {
		t.Fatalf("ask: %v %+v %+v", err, res, got)
	}
}
