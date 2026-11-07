package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		args := map[string]any{"to": target, "body": "network hello", "as_owner": asOwner}
		if !asOwner {
			args["sender_cto"] = true
			args["sender"] = w.ownerID()
		}
		raw, _ := json.Marshal(args)
		result, err := w.d.runCTOTool(context.Background(), "agent_message_send", raw)
		if err != nil {
			t.Fatal(err)
		}
		_ = result
		select {
		case msg := <-peer.Pushes:
			return msg
		case <-time.After(2 * time.Second):
			t.Fatal("missing message")
		}
		return protocol.Message{}
	}
	plain := send(false)
	if plain.From != id || plain.FromCTO || plain.FromHarness != "cto" || plain.OwnerApproved {
		t.Fatalf("plain: %+v", plain)
	}
	w.d.ctoGraph.mu.Lock()
	w.d.ctoGraph.toggle(time.Now())
	w.d.ctoGraph.mu.Unlock()
	approved := send(true)
	if approved.From != id || !approved.FromCTO || !approved.OwnerApproved {
		t.Fatalf("approved: %+v", approved)
	}
	result, err := w.d.readCTOTool(context.Background(), "agent_messages_find", json.RawMessage(`{}`))
	b, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(b), "network hello") {
		t.Fatalf("own messages: %s %v", b, err)
	}
	// A reply belongs to the CTO's conversation, including approved replies.
	incoming := sendResult(t, peer.mustCall("reply", map[string]string{"message_id": plain.ID, "body": "question for CTO"}))
	raw, _ := json.Marshal(map[string]any{"message": incoming.ID, "body": "approved answer", "as_owner": true})
	proposal, err := w.d.runCTOTool(context.Background(), "agent_message_reply", raw)
	if err != nil {
		t.Fatal(err)
	}
	_ = proposal
	select {
	case reply := <-peer.Pushes:
		if reply.From != id || !reply.FromCTO || !reply.OwnerApproved || !strings.Contains(codexFrame(reply), protocol.CTONote) || reply.ParentID != incoming.ID || reply.Body != "approved answer" {
			t.Fatalf("approved reply: %+v", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing approved reply")
	}
	status, err := w.d.readCTOTool(context.Background(), "agent_message_get", json.RawMessage(fmt.Sprintf(`{"message":%q}`, incoming.ID)))
	b, _ = json.Marshal(status)
	if err != nil || !strings.Contains(string(b), "question for CTO") {
		t.Fatalf("own status: %s %v", b, err)
	}

}

func TestCTONetworkRefusesOlderGateway(t *testing.T) {
	t.Parallel()
	for _, caps := range []map[string]bool{nil, {"attribution": true}} {
		t.Run(fmt.Sprint(caps), func(t *testing.T) {
			url, _ := fakeOldGateway(t, caps)
			d, err := New(Config{GatewayURL: url, NodeID: "n_old", Token: "test", StateDir: seededStateDir(t), SocketPath: filepath.Join(shortDir(t), "d.sock"), DetectHarnesses: fakeInventory, WorkerGoCache: t.TempDir(), WorkerGoTmp: t.TempDir()})
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

func TestCTONetworkReplyDelivered(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	peer, target := helloPi(t, w.node, "reply-peer", "/w/peer", true)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	raw, _ := json.Marshal(map[string]string{"to": target, "body": "question"})
	if _, err := w.d.runCTOTool(context.Background(), "agent_message_send", raw); err != nil {
		t.Fatal(err)
	}
	sent := peer.nextPush()
	peer.mustCall("reply", map[string]any{"message_id": sent.ID, "body": "network answer"})
	eventually(t, "network reply in CTO timeline", func() bool { return strings.Contains(string(w.cli.mustCall("cto_events", nil)), "network answer") })
}

type recordingCTOClaude struct {
	*fsClaudeAdapter
	received chan string
}

func (a *recordingCTOClaude) DeliverCTO(ctx context.Context, target protocol.SessionInfo, id, text string) Result {
	result := a.fsClaudeAdapter.DeliverCTO(ctx, target, id, text)
	if result.Delivered {
		a.received <- text
	}
	return result
}

func TestCTOReplyFollowsHarnessSwitch(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	sessions, _ := useFakeClaude(t)
	adapter := &recordingCTOClaude{fsClaudeAdapter: &fsClaudeAdapter{dir: sessions}, received: make(chan string, 10)}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.ClaudeSessionsDir = sessions
		c.Adapters = append(c.Adapters, adapter)
		c.HarnessPaths = map[string]string{"claude": os.Args[0], "opencode": os.Args[0]}
	}})
	cli := cliClient(t, n)
	peer, target := helloPi(t, n, "switch-peer", "/w/peer", true)
	harness := "opencode"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	cli.mustCall("cto_send", map[string]string{"text": "start open"})
	send := func(body string) protocol.Message {
		raw, _ := json.Marshal(map[string]string{"to": target, "body": body})
		if _, err := n.d.runCTOTool(context.Background(), "agent_message_send", raw); err != nil {
			t.Fatal(err)
		}
		return peer.nextPush()
	}
	first := send("from open")
	harness = "claude"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	peer.mustCall("reply", map[string]string{"message_id": first.ID, "body": "to claude"})
	select {
	case text := <-adapter.received:
		if !strings.Contains(text, "to claude") {
			t.Fatalf("Claude got %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reply did not reach Claude")
	}
	second := send("from claude")
	harness = "opencode"
	cli.mustCall("cto_settings", protocol.CTOSettingsRequest{Harness: &harness})
	peer.mustCall("reply", map[string]string{"message_id": second.ID, "body": "to open"})
	eventually(t, "reply in OpenCode", func() bool { return strings.Contains(string(cli.mustCall("cto_events", nil)), "to open") })
}

// approveOwnerMessage approves the proposal behind an as-owner send or reply
// receipt, as the owner would in the Inbox.
func approveOwnerMessage(t *testing.T, d *Daemon, result any) {
	t.Helper()
	var id string
	switch r := result.(type) {
	case messageReceipts:
		if len(r.Sent) == 1 && r.Sent[0].State == "proposed" {
			id = r.Sent[0].MessageID
		}
	case messageReceipt:
		if r.State == "proposed" {
			id = r.MessageID
		}
	}
	if !strings.HasPrefix(id, "p_") {
		t.Fatalf("expected a proposal receipt, got %#v", result)
	}
	if err := d.proposalRunner().decide(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
}
