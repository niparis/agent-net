package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHeldRequestShownInNeeds(t *testing.T) {
	m := newModel(120, 30)
	m.snap.Held = []protocol.Message{{ID: "m_held", FromName: "alice", To: "s_0000000000000002", Body: "please help\nsecond line"}}
	if !strings.Contains(m.View(), "alice ->") || !strings.Contains(m.View(), "please help") || strings.Contains(m.View(), "second line") {
		t.Fatalf("held request missing: %s", m.View())
	}
}

func TestHeldRequestKeys(t *testing.T) {
	for _, key := range []string{"a", "A", "r", "R"} {
		t.Run(key, func(t *testing.T) {
			src := &heldTestSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
			src.snap.Held = []protocol.Message{{ID: "m_held", FromName: "alice", To: "s_0000000000000002", Body: "hello"}}
			m := New(src)
			m.snap = src.snap
			m.selected = "m_held"
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			if cmd == nil {
				t.Fatal("missing command")
			}
			cmd()
			if src.answer.MessageID != "m_held" || src.answer.Action != key {
				t.Fatalf("answer %#v", src.answer)
			}
		})
	}
}

type heldTestSource struct {
	fakeSource
	answer protocol.HeldAnswer
}

func (s *heldTestSource) AnswerHeld(_ context.Context, q protocol.HeldAnswer) error {
	s.answer = q
	return nil
}

func TestHeldAndInboxApprovalBothAppearInNeeds(t *testing.T) {
	m := newModel(120, 30)
	m.snap.Held = []protocol.Message{{ID: "m_held", FromName: "alice", To: "s_2", Body: "please help"}}
	m.inboxItems = []protocol.InboxItem{{ID: "i_1", Kind: "approval", SessionID: "s_1", Text: "deploy abc", Status: "open"}}
	view := m.View()
	if !strings.Contains(view, "please help") || !strings.Contains(view, "deploy abc") || !strings.Contains(view, "Needs you (2)") {
		t.Fatalf("needs: %s", view)
	}
}
