package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestInboxScreenShowsContext(t *testing.T) {
	m := newModel(80, 24)
	m.inboxItems = []protocol.InboxItem{{ID: "i_1", Kind: "approval", Text: "deploy abc", Recommendation: "yes", Status: "open", CreatedAt: time.Now()}}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if !strings.Contains(m.View(), "deploy abc") {
		t.Fatal(m.View())
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "yes") {
		t.Fatal(m.View())
	}
}

func TestApprovalAppearsInNeedsYou(t *testing.T) {
	m := newModel(80, 24)
	m.inboxItems = []protocol.InboxItem{{ID: "i_1", Kind: "approval", SessionID: "s_1", Text: "deploy abc", Status: "open", CreatedAt: time.Now()}}
	if !strings.Contains(m.View(), "deploy abc") || !strings.Contains(m.View(), "inbox 1") {
		t.Fatal(m.View())
	}
}
