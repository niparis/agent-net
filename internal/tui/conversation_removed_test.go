package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The conversation view is gone. c now hands the selected row to the CTO, and
// no key reads session text any more.
func TestCNowHandsTheRowToTheCTO(t *testing.T) {
	m, src := workerModel(100, 30)
	m = selectID(m, workerID)
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if cmd != nil {
		t.Fatal("c started a command")
	}
	if next.handover == nil {
		t.Fatal("c did not open the hand-over preview")
	}
	if !strings.Contains(next.handover.text, "a session row in Sessions") {
		t.Fatalf("preview is not a session row:\n%s", next.handover.text)
	}
	if len(src.queries) != 0 {
		t.Fatalf("c read the session text: %+v", src.queries)
	}
}

// The old outcome shortcut c opened a conversation. c now hands the outcome
// row to the CTO, and R still does nothing.
func TestOutcomeHandOverAndRemovedKey(t *testing.T) {
	m := New(&fakeSource{})
	m.snap = Snapshot{Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r", From: "a", To: "b"}, Sender: "sender", Recipient: "receiver", Request: "deploy?"}}}
	m.selected = "outcome:r"
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if cmd != nil {
		t.Fatal("c started a command")
	}
	if next.handover == nil || !strings.Contains(next.handover.text, "an outcome row in Needs you") {
		t.Fatalf("c did not hand over the outcome row: %+v", next.handover)
	}
	next, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd != nil {
		t.Fatal("R still starts a command")
	}
	if next.View() != m.View() {
		t.Fatal("R changed the screen")
	}
}
