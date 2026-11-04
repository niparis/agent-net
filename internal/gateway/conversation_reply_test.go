package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude reply whose daemon cannot tell which of several open requests it
// answers is threaded to their conversation with no parent (W4.4). It stays
// in that conversation, counts as the answer to the open requests there, and
// is refused from a session that is not part of the conversation.
func TestReplyThreadedToConversationWithoutParent(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	q1, _, err := send(s, b, a, "q1", "")
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "first answer", ParentID: q1.ID})
	if err != nil {
		t.Fatal(err)
	}
	q2, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Type: protocol.TypeRequest, Body: "two", ParentID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	q3, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Type: protocol.TypeRequest, Body: "three", ParentID: first.ID})
	if err != nil {
		t.Fatal(err)
	}

	m, created, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeReply, Body: "both done",
		ConversationID: q1.ConversationID, IdempotencyKey: "claude:x"})
	if err != nil || !created {
		t.Fatalf("threaded reply: created=%v err=%v", created, err)
	}
	if m.ConversationID != q1.ConversationID || m.ParentID != "" || m.Type != protocol.TypeReply {
		t.Fatalf("threaded reply = %+v, want conversation %s, no parent, type reply", m, q1.ConversationID)
	}
	if want := q3.Hop + 1; m.Hop != want {
		t.Fatalf("hop = %d, want %d", m.Hop, want)
	}
	for _, q := range []protocol.Message{q2, q3} {
		got, _, err := s.Message(q.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != protocol.StateReplied {
			t.Fatalf("request %s state = %s, want replied", q.ID, got.State)
		}
	}

	// A session that never wrote to the sender in that conversation cannot
	// post into it.
	n1, _, _ := s.CreateNode("h3", "linux")
	c, err := s.UpsertSession(n1, protocol.SessionInfo{Harness: "claude", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: c, Recipient: b, Type: protocol.TypeReply, Body: "intruder",
		ConversationID: q1.ConversationID}); err == nil {
		t.Fatal("a stranger posted into the conversation")
	}
	// Nor can a message go into it to a session that never wrote there.
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: c, Type: protocol.TypeReply, Body: "x",
		ConversationID: q1.ConversationID}); err == nil {
		t.Fatal("a reply went to a session that never wrote in the conversation")
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeReply, Body: "x",
		ConversationID: "c_unknown"}); err == nil {
		t.Fatal("an unknown conversation was accepted")
	}
}
