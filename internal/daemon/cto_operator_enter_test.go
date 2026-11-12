package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOOperatorEnterOnRemoteOwnerTUI(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	ctoHost := startNode(t, g, nodeOpts{})
	ownerHost := startNode(t, g, nodeOpts{cmdline: "pi"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ctoHost.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	_, sid := helloPi(t, ownerHost, "fixture", "/fixture", true)
	requests := make(chan protocol.UIRequest, 2)
	c, err := client.DialPushData(ownerHost.sock, func(method string, raw json.RawMessage) {
		if method == "cto_ui" {
			var q protocol.UIRequest
			if json.Unmarshal(raw, &q) == nil && q.EnterSession != "" {
				requests <- q
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Call(ctx, "hello", map[string]any{"client": "cli", "tui": true}); err != nil {
		t.Fatal(err)
	}
	second, err := client.DialPushData(ownerHost.sock, func(method string, raw json.RawMessage) {
		if method == "cto_ui" {
			var q protocol.UIRequest
			if json.Unmarshal(raw, &q) == nil && q.EnterSession != "" {
				requests <- q
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Call(ctx, "hello", map[string]any{"client": "cli", "tui": true}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"area": "operator", "method": "session_enter", "arguments": map[string]string{"session_id": sid}})
	if _, err := ctoHost.d.runCTOTool(ctx, "settings_change", args); err != nil {
		t.Fatal(err)
	}
	// Each reply follows any earlier push on the same socket, so both push
	// callbacks have run before counting the requests.
	if _, err := c.Call(ctx, "info", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Call(ctx, "info", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 {
		t.Fatalf("enter reached %d owner TUIs, want one", len(requests))
	}
	select {
	case q := <-requests:
		if q.EnterSession != sid {
			t.Fatalf("wrong session: %+v", q)
		}
	case <-ctx.Done():
		t.Fatal("owner TUI did not get the enter request")
	}
}
