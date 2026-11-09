package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A reply with ack closes the request, reports acknowledged, and delivers
// nothing back to the sender.
func TestReplyAckClosesWithoutDelivery(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "sender known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, piID := helloPi(t, n, "reporter", "/w/reporter", true)
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: piID,
		Type: protocol.TypeRequest, Body: "status report"}); err != nil {
		t.Fatal(err)
	}
	request := pi.nextPush()

	raw, err := pi.call("reply", map[string]any{"message_id": request.ID, "ack": true, "receipt": true})
	if err != nil {
		t.Fatal(err)
	}
	var receipt groupReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.State != protocol.StateAcknowledged || receipt.MessageID == "" {
		t.Fatalf("ack receipt %s: %v", raw, err)
	}
	ack, ok, err := g.gw.Store().Message(receipt.MessageID)
	if err != nil || !ok || ack.Type != protocol.TypeAck || ack.State != protocol.StateAcknowledged {
		t.Fatalf("ack message: %+v %v", ack, err)
	}
	closed, _, err := g.gw.Store().Message(request.ID)
	if err != nil || closed.State != protocol.StateAcknowledged {
		t.Fatalf("request state: %+v %v", closed, err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := fa.delivered(); len(got) != 0 {
		t.Fatalf("ack woke or reached the sender: %+v", got)
	}
}
