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
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: "s_1", Node: "project", Text: "deploy abc", Recommendation: "yes", MessageID: "m_1", ConversationID: "c_1"})
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
	idea, err := s.CreateInboxItem(protocol.InboxItem{Kind: "feature_idea", SessionID: "s_1", Text: "idea"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(idea.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	question, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_1", Text: "question"})
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
