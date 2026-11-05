package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHeldRequestShownInInbox(t *testing.T) {
	m := newModel(120, 30)
	m.snap.Held = []protocol.Message{{ID: "m_held", FromName: "alice", To: "s_0000000000000002", Body: "please help\nsecond line"}}
	m = keys(m, "7")
	if !strings.Contains(m.testView(), "alice") || !strings.Contains(m.testView(), "held request → strategies") || !strings.Contains(m.testView(), "please help") {
		t.Fatalf("held request missing: %s", m.testView())
	}
}

func TestHeldRequestKeys(t *testing.T) {
	for _, key := range []string{"a", "r"} {
		t.Run(key, func(t *testing.T) {
			src := &heldTestSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
			src.snap.Held = []protocol.Message{{ID: "m_held", FromName: "alice", To: "s_0000000000000002", Body: "hello"}}
			m := New(src)
			m.snap = src.snap
			m.tab = inboxTab
			_, cmd := m.Update(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
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

func TestHeldAndInboxApprovalBothAppearInInbox(t *testing.T) {
	m := newModel(120, 30)
	m.snap.Held = []protocol.Message{{ID: "m_held", FromName: "alice", To: "s_2", Body: "please help"}}
	m.inboxItems = []protocol.InboxItem{{ID: "i_1", Kind: "approval", SessionID: "s_1", Text: "deploy abc", Status: "open"}}
	m = keys(m, "7")
	view := m.testView()
	if !strings.Contains(view, "held request →") || !strings.Contains(view, "deploy abc") || !strings.Contains(view, "Needs you") {
		t.Fatalf("inbox: %s", view)
	}
}
