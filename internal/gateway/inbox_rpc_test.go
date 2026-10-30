package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxAnswerGateway(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"id": item.ID, "answer": "yes"})
	g := &Gateway{store: s}
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.answer", raw); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil || len(pending) != 1 || pending[0].Body != "yes" {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
}

func TestInboxCreateUsesAuthenticatedSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Repo: protocol.RepoInfo{Project: "project"}})
	other, _, _ := s.CreateNode("other", "linux")
	g := &Gateway{store: s}
	raw, _ := json.Marshal(map[string]string{"session_id": agent, "kind": "approval", "text": "deploy abc", "recommendation": "yes", "node": "forged"})
	if _, err := g.handle(context.Background(), &nodeConn{id: other}, "inbox.create", raw); err == nil {
		t.Fatal("accepted a session from another node")
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.create", raw); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Node != "project" || items[0].SessionID != agent {
		t.Fatalf("items: %+v %v", items, err)
	}
}

func TestApprovalAnswerHasGatewayNote(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: agent, Text: "deploy abc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil || len(pending) != 1 || !strings.Contains(pending[0].ApprovalNote, "Nicolas approved: deploy abc,") {
		t.Fatalf("pending: %+v %v", pending, err)
	}
}

func TestMessagePreservesBothApprovalFields(t *testing.T) {
	s, _ := newTestStore(t)
	sender, recipient := twoSessions(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: recipient, Body: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET owner_approved=1, approval_note=? WHERE id=?`, "Nicolas approved: action, time", m.ID); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Message(m.ID)
	if err != nil || !ok || !got.OwnerApproved || got.ApprovalNote != "Nicolas approved: action, time" {
		t.Fatalf("message: %+v %v", got, err)
	}
}

func TestMaturityApprovalUsesInboxAndSharedNote(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	for _, action := range []string{"merge project abc123", "deploy project abc123 to prod"} {
		item, err := s.RequestWrittenApproval(agent, "project", protocol.ApprovalRequest{Action: action, Recommendation: "approve"})
		if err != nil {
			t.Fatal(err)
		}
		answered, err := s.AnswerInbox(item.ID, "approve")
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := s.Pending(node, false)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range msgs {
			if m.ApprovalNote == protocol.WrittenApprovalNote("Nicolas", action, answered.AnsweredAt) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no shared note for %s: %+v", action, msgs)
		}
	}
}
