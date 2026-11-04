package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// noticesTo lists agent-net's notices that reached a fake harness.
func noticesTo(a *fakeAdapter) []protocol.Message {
	var out []protocol.Message
	for _, m := range a.delivered() {
		if m.From == "agent-net" {
			out = append(out, m)
		}
	}
	return out
}

// W4.3: a refusal at the receiving harness reaches the sender's harness as one
// notice from agent-net itself, through the real gateway and both daemons.
func TestUndeliveredNoticeReachesSenderEndToEnd(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fb := &fakeAdapter{harness: "fake", fail: "plugin refused: busy"}
	recv := startNode(t, g, nodeOpts{adapter: fb})
	fb.setSessions(fakeSession("nb", "bob"))
	fa := &fakeAdapter{harness: "fake"}
	snd := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	var from, to string
	eventually(t, "both sessions registered", func() bool {
		from, to = snd.d.SessionID("fake", "na"), recv.d.SessionID("fake", "nb")
		return from != "" && to != ""
	})
	m, _, err := g.gw.Store().StoreMessage(protocol.MsgSend{Sender: from, Recipient: to, Body: "please review", IdempotencyKey: "e2e-notice"})
	if err != nil {
		t.Fatal(err)
	}
	recv.d.onDeliver(context.Background(), m)
	eventually(t, "sender's harness gets the notice", func() bool { return len(noticesTo(fa)) == 1 })
	n := noticesTo(fa)[0]
	if n.Type != protocol.TypeNotification || n.FromHarness != "agent-net" || n.FromName != "agent-net" {
		t.Fatalf("notice is not marked as agent-net's own: %+v", n)
	}
	for _, want := range []string{"message to bob was not delivered: plugin refused: busy", m.ID, "please review"} {
		if !strings.Contains(n.Body, want) {
			t.Fatalf("notice %q lacks %q", n.Body, want)
		}
	}
	// The same refusal again changes nothing for the sender.
	recv.d.onDeliver(context.Background(), m)
	if got, _, _ := g.gw.Store().Message(m.ID); got.State != protocol.StateFailed {
		t.Fatalf("state %q", got.State)
	}
	if got := noticesTo(fa); len(got) != 1 {
		t.Fatalf("notices after a second refusal: %d", len(got))
	}
}
