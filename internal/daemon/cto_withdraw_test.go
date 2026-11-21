package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO sends a message to a peer that is not yet delivered, then takes it
// back through its own tool path. Before the CTO dispatch knew the withdraw
// tool, this call returned "unknown CTO tool".
func TestCTOWithdrawsItsUndeliveredMessage(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	_, target := helloPi(t, w.node, "withdraw-peer", "/w/peer", false)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	ctx := context.Background()
	sendRaw, _ := json.Marshal(map[string]string{"to": target, "body": "stale order"})
	res, err := w.d.runCTOToolRaw(ctx, "agent_message_send", sendRaw)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(messageReceipts).Sent[0].MessageID
	withdrawRaw, _ := json.Marshal(map[string]string{"message": id})
	if _, err := w.d.runCTOToolRaw(ctx, "agent_message_withdraw", withdrawRaw); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	m, ok, err := g.gw.Store().Message(id)
	if err != nil || !ok || m.State != protocol.StateWithdrawn {
		t.Fatalf("state after withdraw: %+v ok=%v err=%v", m, ok, err)
	}
}

// Once the peer has the message, the gateway refuses to take it back, and the
// CTO sees that refusal rather than a missing tool.
func TestCTOWithdrawRefusesDeliveredMessage(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	peer, target := helloPi(t, w.node, "delivered-peer", "/w/peer", true)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	ctx := context.Background()
	sendRaw, _ := json.Marshal(map[string]string{"to": target, "body": "delivered order"})
	res, err := w.d.runCTOToolRaw(ctx, "agent_message_send", sendRaw)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(messageReceipts).Sent[0].MessageID
	peer.nextPush()
	eventually(t, "the peer has the message", func() bool {
		m, ok, err := g.gw.Store().Message(id)
		return err == nil && ok && (m.State == protocol.StateDelivered || m.State == protocol.StateAcknowledged)
	})
	withdrawRaw, _ := json.Marshal(map[string]string{"message": id})
	_, err = w.d.runCTOToolRaw(ctx, "agent_message_withdraw", withdrawRaw)
	if err == nil || strings.Contains(err.Error(), "unknown CTO tool") || !strings.Contains(err.Error(), "cannot be withdrawn") {
		t.Fatalf("withdraw of a delivered message: %v", err)
	}
}

// The lead's stale backlog is cleared in one call: older_than takes back every
// undelivered message to the same session that is as old as the named one.
func TestCTOWithdrawsOlderBacklog(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	_, target := helloPi(t, w.node, "backlog-peer", "/w/peer", false)
	w.cli.mustCall("cto_send", map[string]string{"text": "start"})
	ctx := context.Background()
	var ids []string
	for _, body := range []string{"order one", "order two", "order three"} {
		raw, _ := json.Marshal(map[string]string{"to": target, "body": body})
		res, err := w.d.runCTOToolRaw(ctx, "agent_message_send", raw)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.(messageReceipts).Sent[0].MessageID)
	}
	raw, _ := json.Marshal(map[string]any{"message": ids[1], "older_than": true})
	res, err := w.d.runCTOToolRaw(ctx, "agent_message_withdraw", raw)
	if err != nil {
		t.Fatalf("withdraw older_than: %v", err)
	}
	var out struct {
		Withdrawn int `json:"withdrawn"`
	}
	b, _ := json.Marshal(res)
	if json.Unmarshal(b, &out) != nil || out.Withdrawn != 2 {
		t.Fatalf("withdrawn count: %s", b)
	}
	for i, want := range []string{protocol.StateWithdrawn, protocol.StateWithdrawn, protocol.StateDispatched} {
		m, ok, err := g.gw.Store().Message(ids[i])
		if err != nil || !ok || m.State != want {
			t.Fatalf("message %d: %+v ok=%v err=%v", i, m, ok, err)
		}
	}
}
