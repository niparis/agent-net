package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The conversation view is gone. c does nothing on the Sessions tab, and no
// key reads session text any more.
func TestCNoLongerOpensTheConversationView(t *testing.T) {
	m, src := workerModel(100, 30)
	m = selectID(m, workerID)
	before := m.View()
	next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if cmd != nil {
		t.Fatal("c started a command")
	}
	if next.View() != before {
		t.Fatalf("c changed the screen:\n%s", next.View())
	}
	if len(src.queries) != 0 {
		t.Fatalf("c read the session text: %+v", src.queries)
	}
}

// The outcome shortcuts c and R only opened a conversation, so they are gone.
func TestOutcomeConversationKeysAreGone(t *testing.T) {
	m := New(&fakeSource{})
	m.snap = Snapshot{Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r"}, Sender: "sender", Recipient: "receiver"}}}
	m.selected = "outcome:r"
	for _, k := range []string{"c", "R"} {
		next, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if cmd != nil {
			t.Fatalf("%s still starts a command", k)
		}
		if next.View() != m.View() {
			t.Fatalf("%s changed the screen", k)
		}
	}
}
