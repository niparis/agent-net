package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ownerSession makes the owner pseudo-session of a node, as the daemon does.
func ownerSession(t *testing.T, s *Store) string {
	t.Helper()
	n, _, err := s.CreateNode("h-owner", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(n, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID, Name: protocol.OwnerName("h-owner")})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The owner writes into a conversation of the web console: the message is a
// reply in that conversation with no parent, and no new conversation opens.
func TestOwnerSendWithConversationStaysInTheConversation(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	owner := ownerSession(t, s)
	q, _, err := send(s, a, b, "q1", "")
	if err != nil {
		t.Fatal(err)
	}
	m, created, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Type: protocol.TypeRequest, Body: "from the owner", ConversationID: q.ConversationID, IdempotencyKey: "k_owner1"})
	if err != nil || !created {
		t.Fatalf("owner reply: created=%v err=%v", created, err)
	}
	if m.ConversationID != q.ConversationID || m.ParentID != "" || m.Type != protocol.TypeReply || m.Hop != q.Hop+1 {
		t.Fatalf("owner reply = %+v, want conversation %s, no parent, type reply, hop %d", m, q.ConversationID, q.Hop+1)
	}
	msgs, err := s.ConversationMessages(q.ConversationID, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("conversation has %d messages (%v), want 2", len(msgs), err)
	}
	// The sender can be a side of the conversation as well.
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: a, Body: "to the first side", ConversationID: q.ConversationID, IdempotencyKey: "k_owner2"}); err != nil {
		t.Fatalf("reply to the other side: %v", err)
	}
}

func TestOwnerSendWithConversationRefusals(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	owner := ownerSession(t, s)
	q, _, err := send(s, a, b, "q1", "")
	if err != nil {
		t.Fatal(err)
	}
	n3, _, _ := s.CreateNode("h3", "linux")
	c, err := s.UpsertSession(n3, protocol.SessionInfo{Harness: "claude", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Body: "x", ConversationID: "c_nope", IdempotencyKey: "k1"})
	if err == nil || !strings.Contains(err.Error(), "c_nope") {
		t.Fatalf("unknown conversation: %v", err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: c, Body: "x", ConversationID: q.ConversationID, IdempotencyKey: "k2"})
	if err == nil || !strings.Contains(err.Error(), "not part of") {
		t.Fatalf("recipient who is not a side: %v", err)
	}
	if msgs, _ := s.ConversationMessages(q.ConversationID, 10); len(msgs) != 1 {
		t.Fatalf("a refused reply was stored: %d messages", len(msgs))
	}
}

// Without a conversation id an owner send opens a conversation, as before.
func TestOwnerSendWithoutConversationOpensOne(t *testing.T) {
	s, _ := newTestStore(t)
	_, b := twoSessions(t, s)
	owner := ownerSession(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Type: protocol.TypeRequest, Body: "hello", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != protocol.TypeRequest || m.ConversationID == "" || m.Hop != 0 {
		t.Fatalf("owner send = %+v", m)
	}
}
