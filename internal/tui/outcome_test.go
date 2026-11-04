package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

type outcomeTestSource struct {
	fakeSource
	id     string
	action string
}

func (s *outcomeTestSource) ResolveOutcome(_ context.Context, id, action string) error {
	s.id, s.action = id, action
	return nil
}

func TestOutcomeKeys(t *testing.T) {
	src := &outcomeTestSource{}
	m := New(src)
	m.snap = Snapshot{Outcomes: []protocol.OutcomeInboxEntry{{EdgeOutcome: protocol.EdgeOutcome{ReplyID: "r"}, Sender: "sender", Recipient: "receiver"}}}
	m.tab = inboxTab
	for _, tc := range []struct{ key, action string }{{"s", "let_stand"}, {"O", "overrule"}} {
		key, action := tc.key, tc.action
		_, cmd := m.key(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
		if cmd == nil {
			t.Fatalf("%s did not resolve", key)
		}
		result, ok := cmd().(actionMsg)
		if !ok || result.err != nil {
			t.Fatalf("%s result: %+v", key, result)
		}
		if src.id != "r" || src.action != action {
			t.Fatalf("%s resolved %q with action %q, want %q", key, src.id, src.action, action)
		}
	}
}
