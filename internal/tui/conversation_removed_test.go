package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// The conversation view is gone, and b does not read session text.
func TestBDoesNotReadTheSession(t *testing.T) {
	m, src := workerModel(100, 30)
	m = selectID(m, workerID)
	next, cmd := press(m, tea.KeyPressMsg{Text: "b", Code: []rune("b")[0]})
	if cmd != nil {
		t.Fatal("b started a command")
	}
	if next.testView() != m.testView() {
		t.Fatal("b changed the screen")
	}
	if len(src.queries) != 0 {
		t.Fatalf("b read the session text: %+v", src.queries)
	}
}

// b is unused; R refreshes even on an outcome row.
func TestOutcomeRemovedKeys(t *testing.T) {
	m := New(&fakeSource{})
	m.asks = []protocol.Ask{askFromOutcome(protocol.OutcomeInboxEntry{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r", From: "a", To: "b"}, Sender: "sender", Recipient: "receiver", Request: "deploy?"})}
	next, cmd := press(m, tea.KeyPressMsg{Text: "b", Code: []rune("b")[0]})
	if cmd != nil {
		t.Fatal("b started a command")
	}
	if next.testView() != m.testView() {
		t.Fatal("b changed the screen")
	}
	m.fetching = false
	next, cmd = press(m, tea.KeyPressMsg{Text: "R", Code: []rune("R")[0]})
	if cmd == nil || !next.fetching {
		t.Fatal("R did not refresh")
	}
}
