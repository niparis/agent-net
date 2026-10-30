package gateway

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxStore(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, _ := s.CreateNode("test", "linux")
	if _, err := s.UpsertSession(node, owner("test")); err != nil {
		t.Fatal(err)
	}
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: agent, Node: "project", Text: "deploy abc", Recommendation: "yes", MessageID: "m_1", ConversationID: "c_1"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" || item.Status != "open" || item.CreatedAt.IsZero() {
		t.Fatalf("item: %+v", item)
	}
	rows, err := s.ListInbox()
	if err != nil || len(rows) != 1 || rows[0] != item {
		t.Fatalf("rows: %+v, %v", rows, err)
	}
	answered, err := s.AnswerInbox(item.ID, "approve")
	if err != nil || answered.Status != "approved" || answered.AnsweredAt.IsZero() {
		t.Fatalf("answer: %+v, %v", answered, err)
	}
	if _, err := s.AnswerInbox(item.ID, "refuse"); err == nil {
		t.Fatal("answered twice")
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "bad", SessionID: "s_1", Text: "text"}); err == nil {
		t.Fatal("invalid kind")
	}
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: "s_1"}); err == nil {
		t.Fatal("empty action")
	}
	idea, err := s.CreateInboxItem(protocol.InboxItem{Kind: "feature_idea", SessionID: agent, Text: "idea"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(idea.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	question, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(question.ID, "good idea"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListInbox()
	if err != nil || len(rows) != 3 || rows[0].Status != "approved" || rows[1].Status != "approved" || rows[2].Status != "answered" || rows[2].Answer != "good idea" {
		t.Fatalf("answered rows: %+v, %v", rows, err)
	}
}

func TestInboxAnswerQueuesDeliveryAtomically(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, err := s.UpsertSession(node, owner("test"))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "answer"); err != nil {
		t.Fatal(err)
	}
	messages, err := s.Pending(node, false)
	if err != nil || len(messages) != 1 || messages[0].To != agent || messages[0].Body != "answer" {
		t.Fatalf("pending: %+v, %v", messages, err)
	}
	if _, err := s.AnswerInbox(item.ID, "again"); err == nil {
		t.Fatal("answered twice")
	}
	messages, err = s.Pending(node, false)
	if err != nil || len(messages) != 1 {
		t.Fatalf("duplicate: %+v %v", messages, err)
	}
}

func TestInboxAnswerRollsBackWhenDeliveryFails(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_inbox_delivery BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT, 'fail'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "answer"); err == nil {
		t.Fatal("expected delivery failure")
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status != "open" {
		t.Fatalf("items: %+v, %v", items, err)
	}
}
