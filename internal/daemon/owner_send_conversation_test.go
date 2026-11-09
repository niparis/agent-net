package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner send carries an optional conversation id to the gateway: the
// message lands in that conversation as a reply. An unknown conversation is
// refused with the gateway's reason, and a send without an id opens a new one.
func TestOwnerSendCarriesTheConversation(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "session", func() bool { return fa.host.SessionID("fake", "na") != "" })
	id := fa.host.SessionID("fake", "na")
	cli := dialLocal(t, n.sock)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	first := sendResult(t, cli.mustCall("owner_send", protocol.OwnerSendRequest{To: id, Body: "hello alice"}))

	in := sendResult(t, cli.mustCall("owner_send", protocol.OwnerSendRequest{To: id, Body: "owner in the thread", ConversationID: first.ConversationID}))
	if in.ConversationID != first.ConversationID || in.Type != protocol.TypeReply {
		t.Fatalf("owner send = %+v, want a reply in %s", in, first.ConversationID)
	}
	if _, err := cli.call("owner_send", protocol.OwnerSendRequest{To: id, Body: "x", ConversationID: "c_nope"}); err == nil || !strings.Contains(err.Error(), "c_nope") {
		t.Fatalf("unknown conversation: %v", err)
	}
	fresh := sendResult(t, cli.mustCall("owner_send", protocol.OwnerSendRequest{To: id, Body: "a new topic"}))
	if fresh.ConversationID == first.ConversationID {
		t.Fatal("a send without a conversation id joined an old conversation")
	}
}
