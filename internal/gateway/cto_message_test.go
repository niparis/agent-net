package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestCTOSenderIsPersistedAndRequiresOwner(t *testing.T) {
	s, _ := newTestStore(t)
	agent, recipient := twoSessions(t, s)
	node := mustNode(t, s, agent)
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: agent, Recipient: recipient, Body: "forged", FromCTO: true}); err == nil {
		t.Fatal("agent forged CTO sender")
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: recipient, Body: "hello", FromCTO: true})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events("msg.stored")
	if err != nil || len(events) != 1 || !strings.Contains(events[0], "by=cto") {
		t.Fatalf("audit: %v %v", events, err)
	}
	got, ok, err := s.Message(m.ID)
	if err != nil || !ok || !got.FromCTO || got.FromHarness != "cto" {
		t.Fatalf("CTO sender lost: %+v %v", got, err)
	}
}
