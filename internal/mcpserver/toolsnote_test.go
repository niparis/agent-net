package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The stale-tools note is added to the next agent-net tool result, once, and
// never to later results.
func TestStaleToolsNoteShowsOnce(t *testing.T) {
	d := NewDaemon("", nil)
	d.note = "agent-net: your agent-net tools are v0.4.5 (this machine runs v0.5.0). Ask your user to reconnect agent-net."
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := New(func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	},
		"v0.4.5", func(context.Context) string { return d.takeNote() })
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	has := func(res *mcp.CallToolResult) bool {
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok && strings.HasPrefix(tc.Text, "agent-net: your agent-net tools are v0.4.5") {
				return true
			}
		}
		return false
	}
	first, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_list"})
	if err != nil || !has(first) {
		t.Fatalf("first result lacks the note: %v %+v", err, first)
	}
	second, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_list"})
	if err != nil || has(second) {
		t.Fatalf("second result must not repeat the note: %v %+v", err, second)
	}
}

// A hello after a redial brings the note again; it must not show a second time.
func TestHelloNoteIsGivenOnce(t *testing.T) {
	d := NewDaemon("", nil)
	if d.takeNote() != "" {
		t.Fatal("no note before a hello")
	}
	d.noteMu.Lock()
	d.note = "n"
	d.noteMu.Unlock()
	if d.takeNote() != "n" || d.takeNote() != "" {
		t.Error("the note must come out exactly once")
	}
	d.noteMu.Lock()
	d.note = "n"
	d.noteMu.Unlock()
	if d.takeNote() != "" {
		t.Error("a redial must not show the note again")
	}
}
