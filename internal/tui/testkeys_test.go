package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// exec runs a command and feeds its message (or each message of a batch) back
// into the model, the way the program does.
func exec(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = exec(m, c)
		}
	case nil:
	default:
		m = send(m, msg)
	}
	return m
}

// pressKey sends a key and runs the command it returns.
func pressKey(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		msg = tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		msg = tea.KeyMsg{Type: tea.KeyPgDown}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, cmd := m.Update(msg)
	return exec(next.(Model), cmd)
}
