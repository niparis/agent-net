package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOUIBroadcast(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
	call := func(name, args string) (any, error) {
		return n.d.runCTOToolRaw(context.Background(), name, json.RawMessage(args))
	}
	if got, err := call("ui_notice", `{"text":"hello"}`); err != nil || got != "no TUI open" {
		t.Fatalf("no TUI: %v %v", got, err)
	}
	channels := []chan protocol.UIRequest{}
	for range 2 {
		ch := make(chan protocol.UIRequest, 1)
		c, err := client.DialPushData(n.sock, func(method string, raw json.RawMessage) {
			if method == "cto_ui" {
				var q protocol.UIRequest
				if json.Unmarshal(raw, &q) == nil {
					ch <- q
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		if _, err = c.Call(context.Background(), "hello", map[string]any{"client": "cli", "tui": true}); err != nil {
			t.Fatal(err)
		}
		channels = append(channels, ch)
	}
	if _, err := call("ui_show", `{"tab":"map","select":"shop","filter":"api","why":"inspect"}`); err != nil {
		t.Fatal(err)
	}
	for _, ch := range channels {
		select {
		case q := <-ch:
			if q.Tab != "map" || q.Select != "shop" || q.Filter != "api" || q.Why != "inspect" {
				t.Fatalf("bad push: %+v", q)
			}
		case <-time.After(time.Second):
			t.Fatal("missing push")
		}
	}
	if _, err := call("ui_notice", `{"text":"hello"}`); err != nil {
		t.Fatal(err)
	}
	for _, ch := range channels {
		select {
		case q := <-ch:
			if q.Text != "hello" {
				t.Fatalf("notice: %+v", q)
			}
		case <-time.After(time.Second):
			t.Fatal("missing notice")
		}
	}
	if _, err := call("ui_show", `{"tab":"bad"}`); err == nil {
		t.Fatal("invalid tab accepted")
	}
}
