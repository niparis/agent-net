package tui

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
)

type outcomeResolver interface {
	ResolveOutcome(context.Context, string, string) error
}

// outcomeSelected tells whether the selection is a request outcome. Keys that
// also do something else (C opens the CTO) act on the outcome first.
func (m Model) outcomeSelected() bool {
	if m.focus != focusSessions {
		return false
	}
	for _, e := range m.snap.Outcomes {
		if m.selected == "outcome:"+e.ReplyID {
			return true
		}
	}
	return false
}

func (m Model) resolveOutcome(action string) tea.Cmd {
	if m.focus != focusSessions {
		return nil
	}
	id := ""
	for _, e := range m.snap.Outcomes {
		if m.selected == "outcome:"+e.ReplyID {
			id = e.ReplyID
			break
		}
	}
	if id == "" {
		return nil
	}
	src := m.src
	return func() tea.Msg {
		resolver, ok := src.(outcomeResolver)
		if !ok {
			return actionMsg{err: fmt.Errorf("outcome decisions unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		if err := resolver.ResolveOutcome(ctx, id, action); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "Outcome: " + action}
	}
}
