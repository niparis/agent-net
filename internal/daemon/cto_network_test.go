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
		args := map[string]any{"action": "send", "to": target, "body": "network hello", "as_owner": asOwner}
		if !asOwner {
			args["sender_cto"] = true
			args["sender"] = w.ownerID()
		}
		raw, _ := json.Marshal(args)
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
	// A reply belongs to the CTO's conversation, including approved replies.
	incoming := sendResult(t, peer.mustCall("send", map[string]string{"to": id, "body": "question for CTO"}))
	raw, _ := json.Marshal(map[string]any{"action": "reply", "message_id": incoming.ID, "body": "approved answer", "as_owner": true})
	proposal, err := w.d.runCTOTool(context.Background(), "message", raw)
	if err != nil {
		t.Fatal(err)
	}
	approveCTOToolResult(t, w.d, proposal.(string))
	select {
	case reply := <-peer.Pushes:
		if reply.From != id || !reply.FromCTO || reply.ParentID != incoming.ID || reply.Body != "approved answer" {
			t.Fatalf("approved reply: %+v", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing approved reply")
	}
	status, err := w.d.readCTOTool(context.Background(), "message_status", json.RawMessage(fmt.Sprintf(`{"message_id":%q}`, incoming.ID)))
	b, _ = json.Marshal(status)
	if err != nil || !strings.Contains(string(b), "question for CTO") {
		t.Fatalf("own status: %s %v", b, err)
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
			// A reconnect to an older gateway must also refuse sends from a saved ID.
			if _, err := d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,'s_cto')`); err != nil {
				t.Fatal(err)
			}
			for _, approved := range []bool{false, true} {
				_, err := d.send(context.Background(), protocol.MsgSend{Sender: "s_cto", Recipient: "s_peer", Body: "hello", FromCTO: approved})
				if err == nil || !strings.Contains(err.Error(), "upgrade the gateway") {
					t.Fatalf("send (approved=%v): %v", approved, err)
				}
			}
			if d.st.outboxLen() != 0 {
				t.Fatal("CTO send entered the offline outbox")
			}

		})
	}
}

func TestCTOPeerInputDoesNotRegisterSession(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	id := w.d.ctoSessionID()
	other, _, err := g.gw.Store().CreateNode("other", "linux")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := g.gw.Store().RegisterCTOSession(other)
	if err != nil || moved != id {
		t.Fatalf("move: %s %v", moved, err)
	}
	for _, sender := range []string{"peer", ""} {
		if _, err := w.d.ctoSendTracked(context.Background(), "a peer or outcome note", sender); err != nil {
			t.Fatal(err)
		}
		a, _, err := g.gw.Store().Agent(id)
		if err != nil || a.NodeID != other {
			t.Fatalf("peer/note replaced the CTO record: %+v %v", a, err)
		}
	}
}
