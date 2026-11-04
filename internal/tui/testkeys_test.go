package tui

import (
	tea "charm.land/bubbletea/v2"
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
	case toolsModalMsg:
		next, follow := m.Update(msg)
		m = exec(next.(Model), follow)
	default:
		m = send(m, msg)
	}
	return m
}

// pressKey sends a key and runs the command it returns.
func pressKey(m Model, k string) Model {
	var msg tea.KeyPressMsg
	switch k {
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgup":
		msg = tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		msg = tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEsc}
	default:
		msg = tea.KeyPressMsg{Text: k, Code: []rune(k)[0]}
	}
	next, cmd := m.Update(msg)
	return exec(next.(Model), cmd)
}
