package daemon

import (
	"context"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// follow_up_at goes with progress only. A final reply ends the wait, so the
// daemon refuses a time on it. A progress reply accepts one.
func TestReplyFollowUpAtNeedsProgress(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "recipient known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	aliceID := fa.host.SessionID("fake", "na")
	pi, piID := helloPi(t, n, "sender", "/w/sender", true)
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: piID,
		Type: protocol.TypeRequest, Body: "please review"}); err != nil {
		t.Fatal(err)
	}
	request := pi.nextPush()

	if _, err := pi.call("reply", map[string]any{"message_id": request.ID, "body": "done", "follow_up_at": "45m"}); err == nil {
		t.Fatal("a final reply with follow_up_at was accepted; want it refused")
	}
	if _, err := pi.call("reply", map[string]any{"message_id": request.ID, "body": "still working", "progress": true, "follow_up_at": "45m"}); err != nil {
		t.Fatalf("progress reply with follow_up_at: %v", err)
	}
}
