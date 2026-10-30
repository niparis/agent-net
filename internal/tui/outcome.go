package tui

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
)

type outcomeResolver interface {
	ResolveOutcome(context.Context, string, string) error
}

func (m Model) resolveOutcome(action string) tea.Cmd {
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
