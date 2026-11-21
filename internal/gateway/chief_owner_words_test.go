package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ownerWordsRig is a chief rig with the owner's session on the chief's node and
// one ask the chief sent to the owner. The owner holds that ask, so only his
// words can settle it through the chief. The clock moves past the ask, so a
// message sent now comes after it.
func ownerWordsRig(t *testing.T) (s *Store, c *clock, chief, asker, node, owner, ask string) {
	t.Helper()
	s, c, chief, asker, node = chiefRig(t)
	owner = ownerSessionOn(t, s, node)
	ask = chiefAsk(t, s, asker, "Run the PN50 installer step?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: ask, Result: protocol.AskSortOwner, Priority: 1,
		Reason: "needs his word", WhyYou: "only he can run the installer"})
	c.advance(time.Minute)
	return s, c, chief, asker, node, owner, ask
}

func ownerSessionOn(t *testing.T, s *Store, node string) string {
	t.Helper()
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// storeMessageID stores one message and returns its id.
func storeMessageID(t *testing.T, s *Store, p protocol.MsgSend) string {
	t.Helper()
	m, _, err := s.StoreMessage(p)
	if err != nil {
		t.Fatalf("store message %q: %v", p.Body, err)
	}
	return m.ID
}

func closeWithOwnerWords(s *Store, node, chief, ask, words string) error {
	_, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortClose, Reason: "the owner ran the installer", OwnerWordsMessage: words}})
	return err
}

// The owner settles an ask he holds in another chat. His line reaches the
// gateway from his own session. The chief closes the ask and cites that line,
// and the close records it in the ask, the sort history and the Desk log.
func TestChiefClosesOwnerHeldAskOnTheOwnersWords(t *testing.T) {
	s, _, chief, _, node, owner, ask := ownerWordsRig(t)

	// Without the citation the close is refused as before.
	if err := closeWithOwnerWords(s, node, chief, ask, ""); err == nil || !strings.Contains(err.Error(), "not waiting for the chief of staff") {
		t.Fatalf("close of an owner-held ask without his words: %v", err)
	}

	words := storeMessageID(t, s, protocol.MsgSend{Sender: owner, Recipient: chief, Body: "installer step ran", IdempotencyKey: "owner-words-ran"})
	if err := closeWithOwnerWords(s, node, chief, ask, words); err != nil {
		t.Fatalf("close on the owner's words: %v", err)
	}
	status, answer, _ := askRow(t, s, `id=?`, ask)
	if status != "closed" || answer != "chief: the owner ran the installer (on the owner's words in "+words+")" {
		t.Fatalf("status %q answer %q: the answer must name the owner's message", status, answer)
	}
	hist, err := s.AskSortHistory(ask)
	if err != nil {
		t.Fatal(err)
	}
	if last := hist[len(hist)-1]; last.Result != protocol.AskSortClose || last.OwnerMessage != words || last.Sorter != chief {
		t.Fatalf("history %+v: the close must record %s", last, words)
	}
	if !deskLogHas(t, s, "on the owner's words in "+words) {
		t.Fatalf("no Desk log line names the owner's message %s", words)
	}
}

// A CTO message counts only when the CTO sent it on the owner's instructions.
func TestChiefClosesOwnerHeldAskOnTheCTOsOwnerWords(t *testing.T) {
	s, _, chief, _, node, _, ask := ownerWordsRig(t)
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	words := storeMessageID(t, s, protocol.MsgSend{Sender: cto, Recipient: chief, Body: "the owner ran the installer",
		FromCTO: true, OwnerApproved: true, IdempotencyKey: "cto-owner-words"})
	if err := closeWithOwnerWords(s, node, chief, ask, words); err != nil {
		t.Fatalf("close on the CTO's owner words: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, ask); status != "closed" {
		t.Fatalf("status %q, want closed", status)
	}
}

// Only the owner's own words settle his ask. A worker's message, a CTO message
// he did not approve and a message id that does not exist are refused, and the
// ask stays with him.
func TestChiefOwnerWordsRefusesMessagesThatAreNotHisWords(t *testing.T) {
	cases := []struct {
		name string
		send func(t *testing.T, s *Store, cto, asker, chief string) string
		want string
	}{
		{"a worker's message", func(t *testing.T, s *Store, cto, asker, chief string) string {
			return storeMessageID(t, s, protocol.MsgSend{Sender: asker, Recipient: chief, Body: "it ran", IdempotencyKey: "worker-says-ran"})
		}, "is not the owner's words"},
		{"a CTO message he did not approve", func(t *testing.T, s *Store, cto, asker, chief string) string {
			return storeMessageID(t, s, protocol.MsgSend{Sender: cto, Recipient: chief, Body: "it ran", FromCTO: true, IdempotencyKey: "cto-says-ran"})
		}, "is not the owner's words"},
		{"a message id that does not exist", func(t *testing.T, s *Store, cto, asker, chief string) string {
			return "m_missing"
		}, "unknown message"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, chief, asker, node, _, ask := ownerWordsRig(t)
			cto, err := s.RegisterCTOSession(node)
			if err != nil {
				t.Fatal(err)
			}
			words := tc.send(t, s, cto, asker, chief)
			err = closeWithOwnerWords(s, node, chief, ask, words)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("close citing %s: %v, want a refusal with %q", words, err, tc.want)
			}
			if status, _, _ := askRow(t, s, `id=?`, ask); status != "open" {
				t.Fatalf("refused close left status %q", status)
			}
			if next := askNext(t, s, ask); next != protocol.AskNextOwner {
				t.Fatalf("refused close moved the ask to %q", next)
			}
		})
	}
}

// Words sent before the ask opened cannot settle it: the owner had not seen the
// ask when he wrote them.
func TestChiefOwnerWordsMustBeSentAfterTheAskOpened(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	owner := ownerSessionOn(t, s, node)
	early := storeMessageID(t, s, protocol.MsgSend{Sender: owner, Recipient: chief, Body: "installer step ran", IdempotencyKey: "owner-words-early"})
	c.advance(time.Minute)
	ask := chiefAsk(t, s, asker, "Run the PN50 installer step?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: ask, Result: protocol.AskSortOwner, Priority: 1,
		Reason: "needs his word", WhyYou: "only he can run the installer"})
	err := closeWithOwnerWords(s, node, chief, ask, early)
	if err == nil || !strings.Contains(err.Error(), "was sent before ask "+ask+" was opened") {
		t.Fatalf("close citing words from before the ask: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, ask); status != "open" {
		t.Fatalf("refused close left status %q", status)
	}
}

// The owner's words can close an ask only. A merge that cites them is refused
// before the sort runs.
func TestChiefOwnerWordsApplyOnlyToClose(t *testing.T) {
	s, _, chief, _, node, owner, ask := ownerWordsRig(t)
	words := storeMessageID(t, s, protocol.MsgSend{Sender: owner, Recipient: chief, Body: "merge it", IdempotencyKey: "owner-words-merge"})
	_, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortMerge, Into: "ask_other", Reason: "same question", OwnerWordsMessage: words}})
	if err == nil || !strings.Contains(err.Error(), "owner_words_message applies only to close") {
		t.Fatalf("merge with owner words: %v", err)
	}
	if status, _, _ := askRow(t, s, `id=?`, ask); status != "open" {
		t.Fatalf("refused merge left status %q", status)
	}
}

// The chief's undo of its own newest sort undoes a close on the owner's words
// too: the ask is open again and waits for the chief.
func TestChiefUndoReopensOwnerHeldAskClosedOnOwnerWords(t *testing.T) {
	s, _, chief, _, node, owner, ask := ownerWordsRig(t)
	words := storeMessageID(t, s, protocol.MsgSend{Sender: owner, Recipient: chief, Body: "installer step ran", IdempotencyKey: "owner-words-undo"})
	if err := closeWithOwnerWords(s, node, chief, ask, words); err != nil {
		t.Fatalf("close on the owner's words: %v", err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: ask, Result: protocol.AskSortUndo}}); err != nil {
		t.Fatalf("chief undo of the owner-words close: %v", err)
	}
	if status, answer, _ := askRow(t, s, `id=?`, ask); status != "open" || answer != "" {
		t.Fatalf("after undo: status %q answer %q", status, answer)
	}
	if next := askNext(t, s, ask); next != protocol.AskNextChief {
		t.Fatalf("after undo: next %q, want chief", next)
	}
}
