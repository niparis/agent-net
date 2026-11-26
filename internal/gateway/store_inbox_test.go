package gateway

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

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
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
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
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
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
	item, err := storeOldItem(t, s, protocol.InboxItem{
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
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: "s_gone", Text: "which branch?"})
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
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: agent, Text: "merge abc"})
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
	old, err := storeOldItem(t, s, protocol.InboxItem{Kind: "check", SessionID: "s_test", Node: "apps", Text: "Connected: Test (bot)"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: "s_test", Text: "approve?"})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(25 * time.Hour)
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
	_, err := storeOldItem(t, s, want)
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

func inboxStatus(t *testing.T, s *Store, id string) string {
	t.Helper()
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == id {
			return item.Status
		}
	}
	t.Fatalf("item %s not listed", id)
	return ""
}

// A new ask adds to the pending ones. Only replaces retires an earlier ask.
func TestInboxNewAskKeepsEarlierOpenAsks(t *testing.T) {
	for _, kind := range []string{"approval", "question"} {
		s, _ := newTestStore(t)
		first, err := storeOldItem(t, s, protocol.InboxItem{Kind: kind, SessionID: "s_a", Text: "delete the data"})
		if err != nil {
			t.Fatal(err)
		}
		second, err := storeOldItem(t, s, protocol.InboxItem{Kind: kind, SessionID: "s_a", Text: "spend the money"})
		if err != nil {
			t.Fatal(err)
		}
		if got := inboxStatus(t, s, first.ID); got != "open" {
			t.Fatalf("%s: first ask is %q after a second ask without replaces", kind, got)
		}
		if got := inboxStatus(t, s, second.ID); got != "open" {
			t.Fatalf("%s: second ask is %q", kind, got)
		}
	}
}

func TestOtherOpenAsksListsTheSessionsPendingAsks(t *testing.T) {
	s, _ := newTestStore(t)
	a, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: "s_a", Text: "delete the data\nmore detail"})
	b, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: "s_a", Text: "which one?"})
	storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: "s_other", Text: "someone else"})
	c, _ := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: "s_a", Text: "spend the money"})
	got, err := s.OtherOpenAsks("s_a", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.OpenAskRef{{ID: a.ID, Kind: "approval", Text: "delete the data"}, {ID: b.ID, Kind: "question", Text: "which one?"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
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
