package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTONetworkMessages(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	peer, _ := helloPi(t, w.node, "peer", "/w/peer", true)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	caller := w.d.ctoReadConn()
	id, err := caller.boundID()
	if err != nil || !strings.HasPrefix(id, "s_") || id == w.ownerID() {
		t.Fatalf("CTO caller: %s %v", id, err)
	}
	var list protocol.AgentsResult
	json.Unmarshal(w.cli.mustCall("list", nil), &list)
	target := ""
	for _, a := range list.Agents {
		if a.Harness == "pi" {
			target = a.SessionID
		}
	}
	if target == "" {
		t.Fatal("peer missing")
	}
	send := func(asOwner bool) protocol.Message {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "send", "to": target, "body": "network hello", "as_owner": asOwner})
		result, err := w.d.runCTOTool(context.Background(), "message", raw)
		if err != nil {
			t.Fatal(err)
		}
		if asOwner {
			approveCTOToolResult(t, w.d, result.(string))
		}
		select {
		case msg := <-peer.Pushes:
			return msg
		case <-time.After(2 * time.Second):
			t.Fatal("missing message")
		}
		return protocol.Message{}
	}
	plain := send(false)
	if plain.From != id || plain.FromCTO || plain.FromHarness != "cto" {
		t.Fatalf("plain: %+v", plain)
	}
	approved := send(true)
	if approved.From != id || !approved.FromCTO {
		t.Fatalf("approved: %+v", approved)
	}
	result, err := w.d.readCTOTool(context.Background(), "messages", json.RawMessage(`{}`))
	b, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(b), "network hello") {
		t.Fatalf("own messages: %s %v", b, err)
	}
}

func TestCTONetworkRefusesOlderGateway(t *testing.T) {
	for _, caps := range []map[string]bool{nil, {"attribution": true}} {
		t.Run(fmt.Sprint(caps), func(t *testing.T) {
			url, _ := fakeOldGateway(t, caps)
			d, err := New(Config{GatewayURL: url, NodeID: "n_old", Token: "test", StateDir: t.TempDir(), SocketPath: filepath.Join(shortDir(t), "d.sock"), DetectHarnesses: fakeInventory})
			if err != nil {
				t.Fatal(err)
			}
			if err = d.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer d.Stop()
			eventually(t, "old gateway connected", d.Connected)
			err = d.registerCTOSession(context.Background())
			if err == nil || !strings.Contains(err.Error(), "upgrade the gateway") {
				t.Fatalf("registration: %v", err)
			}
			if d.ctoSessionID() != "" {
				t.Fatal("old gateway registered CTO")
			}
		})
	}
}
