package gateway

import (
	"context"
	"encoding/json"
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
