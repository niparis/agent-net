package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The ask that holds a worker start is answered like any ask, but its answer
// reaches the daemon as a decision, not the asking session as a message.
func TestStartOverrideAskAnswers(t *testing.T) {
	s, err := OpenStore(seededStorePath(t, "override.db"))
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
	mk := func(ref string) protocol.InboxItem {
		t.Helper()
		item, err := s.CreateStartOverrideAsk(node, protocol.StartOverrideAsk{Session: agent, StartID: ref, Text: "an agent wants model=m. Confirm, or let the picker choose."})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	item := mk("a_1")
	// With no chief of staff the ask goes to the owner (decision 0151).
	var next string
	if err := s.db.QueryRow(`SELECT next_who FROM inbox_items WHERE id=?`, item.ID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if item.Kind != protocol.AskKindQuestion || next != protocol.AskNextOwner || len(item.Options) != 2 {
		t.Fatalf("ask: %+v next %q", item, next)
	}
	if ref := s.startOverrideRef(item.ID); ref != protocol.StartOverrideRefPrefix+"a_1" {
		t.Fatalf("source ref = %q", ref)
	}
	got, err := s.AnswerInboxAs(item.ID, protocol.StartAnswerConfirm, "owner")
	if err != nil || got.Status != "answered" || got.Answer != protocol.StartAnswerConfirm {
		t.Fatalf("owner confirm: %+v, %v", got, err)
	}
	if msgs, _ := s.Messages(node, protocol.QueryMessages{SessionID: agent, Limit: 10}); len(msgs) != 0 {
		t.Fatalf("the answer was sent to the asking session as a message: %+v", msgs)
	}
	if _, err := s.AnswerInboxAs(item.ID, protocol.StartAnswerPicker, "owner"); err == nil {
		t.Fatal("answered twice")
	}
	item = mk("a_2")
	if got, err = s.AnswerInboxAs(item.ID, "dismiss", "owner"); err != nil || got.Status != "dismissed" || got.Answer != "dismiss" {
		t.Fatalf("dismiss: %+v, %v", got, err)
	}
	item = mk("a_3")
	if got, err = s.AnswerInboxAs(item.ID, protocol.StartAnswerPicker, "cto"); err != nil || got.Answer != protocol.StartAnswerPicker {
		t.Fatalf("the CTO chose the picker: %+v, %v", got, err)
	}
	// The ask is not closed when the owner types in the asking session.
	item = mk("a_4")
	if err := s.closeOwnerRepliedAsks(agent, "ok thanks"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListInbox()
	open := false
	for _, r := range rows {
		open = open || (r.ID == item.ID && r.Status == "open")
	}
	if !open {
		t.Fatal("typing in the asking session closed the override ask")
	}
}
