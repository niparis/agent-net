package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestDueForwardsTheCallerSession(t *testing.T) {
	t.Parallel()
	n, f := startDeliveryNode(t)
	f.answer(protocol.FrameDue, func(raw json.RawMessage) (any, error) {
		return protocol.DueResult{Items: []protocol.DueItem{{ID: "d_1", What: "flip", Status: "pending", Blocker: "none", CreatedAt: "2026-11-21T07:00:00Z"}}}, nil
	})
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-chief", wt, "gpt-6.1-sol")
	res, err := lc.call(protocol.OpDue, map[string]any{"action": "add", "what": "flip", "due": "45m"})
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.DueResult
	if err := json.Unmarshal(res, &out); err != nil || len(out.Items) != 1 || out.Items[0].ID != "d_1" {
		t.Fatalf("result = %s (%v)", res, err)
	}
	sent := f.frames(protocol.FrameDue)
	if len(sent) != 1 {
		t.Fatalf("frames sent = %d, want 1", len(sent))
	}
	var q protocol.DueRequest
	if err := json.Unmarshal(sent[0], &q); err != nil {
		t.Fatal(err)
	}
	if q.SessionID != sid || q.Action != protocol.DueActionAdd || q.What != "flip" || q.Due != "45m" {
		t.Fatalf("frame = %+v, caller session %s", q, sid)
	}
}

func TestDueRefusesBadArgsBeforeTheGateway(t *testing.T) {
	t.Parallel()
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, _ := helloDelivery(t, n, "np-chief", wt, "gpt-6.1-sol")
	if _, err := lc.call(protocol.OpDue, map[string]any{"action": "add", "what": "flip"}); err == nil {
		t.Fatal("an add with neither due nor after was accepted")
	}
	if sent := f.frames(protocol.FrameDue); len(sent) != 0 {
		t.Fatalf("a refused call reached the gateway: %d frames", len(sent))
	}
}

// agent_due done takes the item id as "id" through the MCP server. The daemon
// sends the item to the gateway in a frame that owns "id", so it must use
// another name. This runs on the real gateway, which encodes the frame.
func TestDueDoneThroughTheMCPServer(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{adapter: &fakeAdapter{harness: "fake"}, cmdline: "pi"})
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	defer cancel()
	md := mcpserver.NewDaemon(n.sock, map[string]any{"client": "pi", "harness": "pi", "native_id": "np-due-mcp", "name": "due", "cwd": t.TempDir(), "status": "idle"})
	t.Cleanup(md.Close)
	if _, err := md.Call(ctx, "info", nil); err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	server, err := mcpserver.New(md.Call, "").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	// The session appears on the node once its first call has registered it.
	eventually(t, "my session", func() bool {
		_, err := md.Call(ctx, "info", nil)
		return err == nil && len(n.d.Sessions()) > 0
	})

	call := func(args map[string]any) protocol.DueResult {
		t.Helper()
		res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "agent_due", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("agent_due %v: err %v, error text %s", args, err, toolText(res))
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var out protocol.DueResult
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("agent_due %v answer %s: %v", args, raw, err)
		}
		return out
	}
	added := call(map[string]any{"action": "add", "what": "flip", "due": "45m"})
	if len(added.Items) != 1 || added.Items[0].ID == "" {
		t.Fatalf("add returned %+v", added)
	}
	done := call(map[string]any{"action": "done", "id": added.Items[0].ID})
	if len(done.Items) != 1 || done.Items[0].ID != added.Items[0].ID || done.Items[0].Status != "done" {
		t.Fatalf("done returned %+v, want item %s marked done", done, added.Items[0].ID)
	}
}

// toolText returns the text of a tool result, which carries a tool error.
func toolText(res *mcp.CallToolResult) string {
	var out []string
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			out = append(out, text.Text)
		}
	}
	return strings.Join(out, " ")
}
