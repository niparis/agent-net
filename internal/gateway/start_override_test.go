package gateway

import (
	"bytes"
	"context"
	"log"
	"strings"
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

// A held start takes one of its own answers. Free text that names confirm and
// picker together, or any other answer, is refused before it is stored, so a
// start is never read as an option the human did not choose.
func TestStartOverrideAskRefusesAnyOtherAnswer(t *testing.T) {
	s, err := OpenStore(seededStorePath(t, "override.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, _ := s.CreateNode("test", "linux")
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Repo: protocol.RepoInfo{Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateStartOverrideAsk(node, protocol.StartOverrideAsk{Session: agent, StartID: "a_text",
		Text: "an agent wants machine=research. Confirm, or let the picker choose.", OverrideFacts: protocol.OverrideFacts{Harness: "opencode", Model: "fake/m2"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{
		"Confirm machine=research (a non-Claude provider). Picker chooses a non-Claude provider with quota.",
		"confirm please",
		"Picker",
		"yes",
	} {
		got, err := s.AnswerInboxAs(item.ID, answer, "cto")
		if err == nil {
			t.Fatalf("answer %q was accepted as %q", answer, got.Answer)
		}
		for _, want := range []string{protocol.StartAnswerConfirm, protocol.StartAnswerPicker, "dismiss"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("answer %q: the refusal does not list %q: %v", answer, want, err)
			}
		}
		var status, stored string
		if err := s.db.QueryRow(`SELECT status,answer FROM inbox_items WHERE id=?`, item.ID).Scan(&status, &stored); err != nil {
			t.Fatal(err)
		}
		if status != "open" || stored != "" {
			t.Fatalf("answer %q changed the ask to %q %q", answer, status, stored)
		}
	}
	if got, err := s.AnswerInboxAs(item.ID, protocol.StartAnswerConfirm, "owner"); err != nil || got.Status != "answered" {
		t.Fatalf("the real answer was refused: %+v, %v", got, err)
	}
}

// A decision that cannot reach the machine holding the start still tells the
// caller: the gateway stores one not-started notice that names the reason.
func TestStartDecisionToOfflineNodeTellsTheCaller(t *testing.T) {
	s, err := OpenStore(seededStorePath(t, "override.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, _ := s.CreateNode("test", "linux")
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Repo: protocol.RepoInfo{Project: "shop"}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateStartOverrideAsk(node, protocol.StartOverrideAsk{Session: agent, StartID: "a_off",
		Text: "an agent wants harness=opencode model=fake/m2. Confirm, or let the picker choose.", OverrideFacts: protocol.OverrideFacts{Harness: "opencode", Model: "fake/m2"}})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := s.AnswerInboxAs(item.ID, protocol.StartAnswerConfirm, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	g := &Gateway{store: s, conns: map[string]*nodeConn{}, logger: log.New(&logs, "", 0)}
	g.sendStartDecision(context.Background(), answered, s.startOverrideRef(answered.ID))

	var body string
	if err := s.db.QueryRow(`SELECT body FROM messages WHERE recipient=? AND sender='agent-net'`, agent).Scan(&body); err != nil {
		t.Fatalf("the caller got no notice: %v", err)
	}
	for _, want := range []string{"status: not started", "start: a_off", "offline", "shop", "opencode"} {
		if !strings.Contains(body, want) {
			t.Fatalf("notice lacks %q:\n%s", want, body)
		}
	}
	if !strings.Contains(logs.String(), "start_decision.offline") {
		t.Fatalf("no log line for the undelivered decision: %s", logs.String())
	}
	// A second send, or the machine's own later report, does not add a notice.
	g.sendStartDecision(context.Background(), answered, s.startOverrideRef(answered.ID))
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient=? AND sender='agent-net'`, agent).Scan(&count); err != nil || count != 1 {
		t.Fatalf("notices = %d, %v, want one", count, err)
	}
}
