package gateway

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxStore(t *testing.T) {
	s, err := OpenStore(seededStorePath(t, "inbox.db"))
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
	if err == nil && len(rows) == 1 {
		item.AskerName = rows[0].AskerName
	}
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], item) {
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

// The TUI dismisses an item with "dismiss". An approval from agent_owner_ask
// names the session that asked, and that session may be gone by the time the
// owner clears the old row. A dismissal closes the item without finding a
// recipient or queueing a message.
func TestInboxDismissClosesAnApprovalWhoseSessionIsGone(t *testing.T) {
	s, _ := newTestStore(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{
		Kind: "approval", SessionID: "s_gone", Node: "wbg-procurement",
		Text: "merge the procurement branch", Recommendation: "yes",
	})
	if err != nil {
		t.Fatal(err)
	}
	dismissed, err := s.AnswerInbox(item.ID, "dismiss")
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if dismissed.Status != "dismissed" || dismissed.Answer != "" {
		t.Fatalf("dismissed item: %+v", dismissed)
	}
	rows, err := s.ListInbox()
	if err != nil || len(rows) != 1 || rows[0].Status != "dismissed" {
		t.Fatalf("rows: %+v, %v", rows, err)
	}
	var messages int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("dismiss queued %d messages: %v", messages, err)
	}
}

func TestInboxDismissOfAMissingItemSaysNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.AnswerInbox("i_gone", "dismiss"); err == nil || !strings.Contains(err.Error(), "inbox item not found") {
		t.Fatalf("dismiss of a missing item: %v", err)
	}
}

func TestInboxDismissTwiceSaysAlreadyAnswered(t *testing.T) {
	s, _ := newTestStore(t)
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_gone", Text: "which branch?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "dismiss"); err == nil || !strings.Contains(err.Error(), "inbox item already answered") {
		t.Fatalf("second dismiss: %v", err)
	}
}

// A dismissal clears the row and sends nothing to the session, unlike an
// answer, which is delivered.
func TestInboxDismissDoesNotMessageTheSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: agent, Text: "merge abc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil || len(pending) != 0 {
		t.Fatalf("dismiss queued %d messages: %v", len(pending), err)
	}
}

func TestInboxConnectedNoticeExpiresAfterDay(t *testing.T) {
	s, c := newTestStore(t)
	old, err := s.CreateInboxItem(protocol.InboxItem{Kind: "check", SessionID: "s_test", Node: "apps", Text: "Connected: Test (bot)"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: "s_test", Text: "approve?"})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(25 * time.Hour)
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == old.ID {
			t.Fatal("old connected notice still visible")
		}
	}
	if len(items) != 1 || items[0].Kind != "approval" {
		t.Fatalf("items: %+v", items)
	}
}

func TestInboxFieldsRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	want := protocol.InboxItem{Kind: "question", SessionID: "s_test", Text: "Which branch?", Recommendation: "Use main", Options: []string{"main", "next"}, AskerName: "Build agent", ChangeTitle: "Fix build"}
	_, err := s.CreateInboxItem(want)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v %v", items, err)
	}
	got := items[0]
	if got.Text != want.Text || got.Recommendation != want.Recommendation || !reflect.DeepEqual(got.Options, want.Options) || got.AskerName != want.AskerName || got.ChangeTitle != want.ChangeTitle {
		t.Fatalf("got %+v", got)
	}
}

func TestInboxReplacementAndTextLimit(t *testing.T) {
	s, _ := newTestStore(t)
	first, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_a", Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: "s_a", Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == first.ID && item.Status != "replaced" {
			t.Fatalf("not replaced: %+v", item)
		}
	}
	_, err = s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: "s_a", Text: strings.Repeat("é", 4001)})
	if err == nil {
		t.Fatal("accepted question over 4000 characters")
	}
}

func TestInboxWorkerEndClosesItems(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "worker"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: sid, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: sid, Harness: "pi", Outcome: protocol.OutcomeComplete})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == item.ID && r.Status == "open" {
			t.Fatal("ended worker item remains open")
		}
	}
}
func TestInboxArchivedSessionClosesItems(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	sid, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "archived"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: sid, Text: "approval"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CloseArchivedSessionInbox(node, sid); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == item.ID && r.Status == "open" {
			t.Fatal("archived session item remains open")
		}
	}
}

func TestInboxOwnerIdeaTextLimit(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "feature_idea", SessionID: "s_a", Text: strings.Repeat("x", 4001)}); err == nil {
		t.Fatal("accepted idea beyond owner ask limit")
	}
}
