package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOUIBroadcast(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
	call := func(name, args string) (any, error) {
		return n.d.runCTOToolRaw(context.Background(), name, json.RawMessage(args))
	}
	if got, err := call("cto_ui_show", `{"notice":"hello"}`); err != nil || got != "no TUI open" {
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
	if _, err := call("cto_ui_show", `{"tab":"graph","select":"shop","filter":"api","why":"inspect"}`); err != nil {
		t.Fatal(err)
	}
	for _, ch := range channels {
		select {
		case q := <-ch:
			if q.Tab != "graph" || q.Select != "shop" || q.Filter != "api" || q.Why != "inspect" {
				t.Fatalf("bad push: %+v", q)
			}
		case <-time.After(time.Second):
			t.Fatal("missing push")
		}
	}
	if _, err := call("cto_ui_show", `{"notice":"hello"}`); err != nil {
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
	// daemon_notice and herdr_opened are daemon-only fields of
	// protocol.UIRequest, not arguments of this tool. A CTO call that names
	// one is refused by name and shows nothing.
	for _, field := range []string{"daemon_notice", "herdr_opened"} {
		if _, err := call("cto_ui_show", `{"notice":"hello","`+field+`":true}`); err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("%s from the CTO: %v", field, err)
		}
		for _, ch := range channels {
			select {
			case q := <-ch:
				t.Fatalf("%s showed %+v", field, q)
			default:
			}
		}
	}
	// A flag the daemon itself sets still reaches the UI.
	if got := n.d.broadcastCTOUI(protocol.UIRequest{Text: "session started", DaemonNotice: true, HerdrOpened: true}); got != len(channels) {
		t.Fatalf("daemon notice reached %d of %d TUIs", got, len(channels))
	}
	for _, ch := range channels {
		select {
		case q := <-ch:
			if q.Text != "session started" || !q.DaemonNotice || !q.HerdrOpened {
				t.Fatalf("daemon notice: %+v", q)
			}
		case <-time.After(time.Second):
			t.Fatal("missing daemon notice")
		}
	}
	if _, err := call("cto_ui_show", `{"tab":"bad"}`); err == nil {
		t.Fatal("invalid tab accepted")
	}
}
