package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/toolcatalog"
)

func TestCTOOutputFromNewerDaemon(t *testing.T) {
	// Model an MCP binary whose embedded machine schema predates health.
	for i := range toolcatalog.Tools {
		if toolcatalog.Tools[i].Name != "agent_machine_get" {
			continue
		}
		old := toolcatalog.Tools[i].OutputSchema
		var schema map[string]any
		if err := json.Unmarshal(old, &schema); err != nil {
			t.Fatal(err)
		}
		delete(schema["properties"].(map[string]any), "health")
		toolcatalog.Tools[i].OutputSchema, _ = json.Marshal(schema)
		t.Cleanup(func() { toolcatalog.Tools[i].OutputSchema = old })
		break
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := NewCTO(func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"machine":{"id":"m1","name":"test","online":true,"version":"v1.0.0","harnesses":[{"name":"test","version":"1","new":1}],"sessions_online":0,"workers_running":0,"new":2},"models":{"items":[{"harness":"test","id":"m","new":3}],"total":1,"new":4},"health":{"status":"ok"}}`), nil
	}, "v0.4.149")
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
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_machine_get", Arguments: map[string]any{"machine": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("new daemon output rejected: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), `"new"`) || strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"new"`) || strings.Contains(string(raw), `"health"`) || strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"health"`) {
		t.Fatalf("unknown health leaked: %s", raw)
	}
}

func TestCatalogOutputCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		bad       bool
	}{
		{"nested defs", `{"sent":[{"message_id":"m1","to":{"id":"s1","name":"test","new":1},"state":"queued","new":2}],"new":3}`, false},
		{"type mismatch", `{"sent":[{"message_id":42,"to":{"id":"s1","name":"test"},"state":"queued","new":2}]}`, true},
		{"missing required", `{"sent":[{"to":{"id":"s1","name":"test"},"state":"queued","new":2}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := catalogResult("agent_message_send", json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError != tc.bad {
				t.Fatalf("IsError=%v: %+v", res.IsError, res.Content)
			}
			if !tc.bad && strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"new"`) {
				t.Fatal("unknown nested fields leaked")
			}
		})
	}
}

func TestCTOUpgradeHintOnce(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	var note staleNote
	note.observeVersion("v0.4.150", "v0.4.149")
	note.observeVersion("v0.4.150", "v0.4.150")
	if note.takeNote() != "" {
		t.Fatal("hint for an equal or older daemon")
	}
	note.observeVersion("v0.4.149", "v0.4.150")
	srv := NewCTO(func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}, "v0.4.149", func(context.Context) string { return note.takeNote() })
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
	for i := 0; i < 2; i++ {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "agent_machine_get", Arguments: map[string]any{"machine": "test"}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "v0.4.150") {
				found = true
			}
		}
		if found != (i == 0) {
			t.Fatalf("call %d: hint=%v", i+1, found)
		}
		note.observeVersion("v0.4.149", "v0.4.151") // A reconnect must not repeat it.
	}
}
