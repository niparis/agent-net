package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestCTOToolsForward(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	seen := map[string]bool{}
	srv := NewCTO(func(_ context.Context, method string, params any) (json.RawMessage, error) {
		seen[method] = true
		return json.RawMessage(`{"ok":true}`), nil
	}, "test")
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
	for _, name := range []string{"graph_get", "charter_set", "charter_history", "node_add", "node_remove", "edge_set", "edge_remove", "edge_history", "graph_undo", "agents", "workers", "needs_you", "feed", "send", "worker_start"} {
		out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || out.IsError || !seen[name] {
			t.Errorf("%s: %+v %v", name, out, err)
		}
	}
}
