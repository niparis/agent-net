package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOReplyReachesConversationAndOwner(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) { c.HarnessPaths = map[string]string{"opencode": os.Args[0]} }})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "start"})
	peer, peerID := helloPi(t, n, "cto-reply-peer", "/work/peer", true)
	send := func(cto bool, body string) protocol.Message {
		t.Helper()
		raw, err := n.d.send(context.Background(), protocol.MsgSend{Sender: n.d.ownerSessionID(), Recipient: peerID, Body: body, FromCTO: cto})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(raw)
		return sendResult(t, b)
	}
	original := send(true, "CTO request")
	peer.nextPush()
	reply := sendResult(t, peer.mustCall("reply", map[string]any{"message_id": original.ID, "body": "peer answer"}))
	eventually(t, "reply in CTO timeline", func() bool { return strings.Contains(string(cli.mustCall("cto_events", nil)), "peer answer") })
	if timeline := string(cli.mustCall("cto_events", nil)); !strings.Contains(timeline, "Peer message (model output, not the owner's words)") {
		t.Fatalf("missing peer attribution: %s", timeline)
	}
	var inbox struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(cli.mustCall("messages", protocol.QueryMessages{SessionID: n.d.ownerSessionID()}), &inbox); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range inbox.Messages {
		if m.ID == reply.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("reply missing from owner messages")
	}
	personal := send(false, "personal request")
	peer.nextPush()
	personalReply := sendResult(t, peer.mustCall("reply", map[string]any{"message_id": personal.ID, "body": "personal answer"}))
	eventually(t, "personal reply delivered", func() bool { return messageState(t, peer, personalReply.ID) == protocol.StateDelivered })
	if err := json.Unmarshal(cli.mustCall("messages", protocol.QueryMessages{SessionID: n.d.ownerSessionID()}), &inbox); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, m := range inbox.Messages {
		if m.ID == personalReply.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("personal reply missing from owner messages")
	}
	if strings.Contains(string(cli.mustCall("cto_events", nil)), "personal answer") {
		t.Fatal("personal reply reached CTO")
	}
}

// TestClaudeAdapterFactory is registered by the external test package to avoid
// an import cycle between daemon and the Claude adapter.
var TestClaudeAdapterFactory func(string, string) Adapter

func TestCTORelayReplyReachesConversationAndOwner(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	dir := shortDir(t)
	sessions, replies := filepath.Join(dir, "sessions"), filepath.Join(dir, "r")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	inboxPath := filepath.Join(dir, "claude.sock")
	ln, err := net.Listen("unix", inboxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	session := fmt.Sprintf(`{"pid":%d,"sessionId":"relay-peer","cwd":"/work/relay","name":"relay peer","status":"idle","messagingSocketPath":%q}`, os.Getpid(), inboxPath)
	if err := os.WriteFile(filepath.Join(sessions, fmt.Sprintf("%d.json", os.Getpid())), []byte(session), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour, cfg: func(c *Config) {
		c.HarnessPaths = map[string]string{"opencode": os.Args[0]}
		c.Adapters = []Adapter{TestClaudeAdapterFactory(sessions, replies)}
	}})
	cli := cliClient(t, n)
	cli.mustCall("cto_send", map[string]string{"text": "start"})
	var peerID string
	eventually(t, "Claude peer registered", func() bool { peerID = n.d.SessionID("claude", "relay-peer"); return peerID != "" })
	raw, err := n.d.send(context.Background(), protocol.MsgSend{Sender: n.d.ownerSessionID(), Recipient: peerID, Body: "CTO request", FromCTO: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw)
	original := sendResult(t, b)
	if err := ln.(*net.UnixListener).SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(conn).ReadString('\n')
	conn.Close()
	relay, err := net.Dial("unix", filepath.Join(replies, n.d.ownerSessionID()+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := json.Marshal(map[string]any{"type": "user", "msgV": 1, "msg_id": "relay-test", "from": "uds:" + inboxPath, "message": map[string]any{"role": "user", "content": "relay peer answer"}})
	if _, err := relay.Write(append(frame, '\n')); err != nil {
		t.Fatal(err)
	}
	relay.Close()
	eventually(t, "relay reply in CTO timeline", func() bool { return strings.Contains(string(cli.mustCall("cto_events", nil)), "relay peer answer") })
	var inbox struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(cli.mustCall("messages", protocol.QueryMessages{SessionID: n.d.ownerSessionID()}), &inbox); err != nil {
		t.Fatal(err)
	}
	for _, m := range inbox.Messages {
		if m.ParentID == original.ID && m.Body == "relay peer answer" {
			return
		}
	}
	t.Fatal("relay reply missing from owner messages")
}
