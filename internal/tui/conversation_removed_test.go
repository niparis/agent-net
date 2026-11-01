package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The conversation view is gone, and b does not read session text.
func TestBDoesNotReadTheSession(t *testing.T) {
	m, src := workerModel(100, 30)
	m = selectID(m, workerID)
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if cmd != nil {
		t.Fatal("b started a command")
	}
	if next.View() != m.View() {
		t.Fatal("b changed the screen")
	}
	if len(src.queries) != 0 {
		t.Fatalf("b read the session text: %+v", src.queries)
	}
}

// Neither b nor R acts on an outcome row.
func TestOutcomeRemovedKeys(t *testing.T) {
	m := New(&fakeSource{})
	m.snap = Snapshot{Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r", From: "a", To: "b"}, Sender: "sender", Recipient: "receiver", Request: "deploy?"}}}
	m.selected = "outcome:r"
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if cmd != nil {
		t.Fatal("b started a command")
	}
	if next.View() != m.View() {
		t.Fatal("b changed the screen")
	}
	next, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if cmd != nil {
		t.Fatal("R still starts a command")
	}
	if next.View() != m.View() {
		t.Fatal("R changed the screen")
	}
}
