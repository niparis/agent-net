package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestOutcomeKeys(t *testing.T) {
	m := New(&fakeSource{})
	m.snap = Snapshot{Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r"}, Sender: "sender", Recipient: "receiver"}}}
	m.selected = "outcome:r"
	for _, key := range []string{"s", "o"} {
		_, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd == nil {
			t.Fatalf("%s did not resolve", key)
		}
	}
}
