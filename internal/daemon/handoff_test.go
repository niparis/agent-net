package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A lead hands a request to a member. The member gets the original sender, its
// reply goes to the asker, and the lead gets a copy.
func TestHandoffEndToEnd(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"), fakeSession("nb", "bob"))
	eventually(t, "recipients known", func() bool {
		return fa.host.SessionID("fake", "na") != "" && fa.host.SessionID("fake", "nb") != ""
	})
	aliceID, bobID := fa.host.SessionID("fake", "na"), fa.host.SessionID("fake", "nb")
	lead, leadID := helloPi(t, n, "lead", "/w/lead", true)
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: leadID,
		Type: protocol.TypeRequest, Body: "please ingest the file"}); err != nil {
		t.Fatal(err)
	}
	request := lead.nextPush()

	raw, err := lead.call("handoff", map[string]any{"message_id": request.ID, "to_session": "bob", "note": "this is yours"})
	if err != nil {
		t.Fatal(err)
	}
	var receipt groupReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.MessageID == "" || receipt.To.ID != bobID || receipt.To.Name != "bob" {
		t.Fatalf("handoff receipt %s: %v", raw, err)
	}
	eventually(t, "bob got the hand-off", func() bool { return len(fa.delivered()) == 1 })
	got := fa.delivered()[0]
	if got.To != bobID || got.From != aliceID || got.ConversationID != request.ConversationID ||
		!strings.Contains(got.Body, "this is yours") || !strings.Contains(got.Body, "please ingest the file") {
		t.Fatalf("hand-off as delivered: %+v", got)
	}

	// Bob answers. The reply goes to alice, and the lead gets a copy.
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: bobID, Recipient: aliceID,
		Type: protocol.TypeReply, ParentID: got.ID, Body: "ingested"}); err != nil {
		t.Fatal(err)
	}
	copyMsg := lead.nextPush()
	if copyMsg.From != bobID || copyMsg.To != leadID || copyMsg.Type != protocol.TypeNotification || !strings.Contains(copyMsg.Body, "ingested") {
		t.Fatalf("lead copy: %+v", copyMsg)
	}
	eventually(t, "alice got the reply", func() bool {
		for _, m := range fa.delivered() {
			if m.To == aliceID && m.From == bobID && m.Body == "ingested" {
				return true
			}
		}
		return false
	})
}

func TestHandoffRefusals(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	fa.setSessions(fakeSession("na", "alice"))
	eventually(t, "alice known", func() bool { return fa.host.SessionID("fake", "na") != "" })
	lead, leadID := helloPi(t, n, "lead", "/w/lead", true)
	aliceID := fa.host.SessionID("fake", "na")
	if err := fa.host.Send(context.Background(), protocol.MsgSend{Sender: aliceID, Recipient: leadID, Type: protocol.TypeRequest, Body: "q"}); err != nil {
		t.Fatal(err)
	}
	request := lead.nextPush()
	for name, args := range map[string]map[string]any{
		"no message": {"to_session": "alice", "note": "x"},
		"no session": {"message_id": request.ID, "note": "x"},
		"unknown":    {"message_id": request.ID, "to_session": "nobody", "note": "x"},
	} {
		if _, err := lead.call("handoff", args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
