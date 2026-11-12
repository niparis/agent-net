package tui

import (
	tea "charm.land/bubbletea/v2"
)

// wheelLines is how many lines one wheel notch scrolls in a conversation.
const wheelLines = 3

// messagesMouseActive says whether the Messages tab asks the terminal for
// mouse events: only when nothing else on the tab has the screen.
func (m Model) messagesMouseActive() bool {
	return m.tab == messagesTab && m.focus != focusCTO && m.mode == modeNormal &&
		!m.help && !m.mapOn && m.open == nil && !m.finder && !m.filtering
}

// messagesMouse turns the wheel into scrolling: over the list it moves the
// selected conversation, over the conversation it scrolls the messages.
func (m Model) messagesMouse(msg tea.MouseMsg) Model {
	wheel, ok := msg.(tea.MouseWheelMsg)
	if !ok {
		return m
	}
	dir := 0
	switch wheel.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	default:
		return m
	}
	g := messagesLayout(max(m.width, 40), m.messagesBodyHeight())
	x, y := wheel.X, wheel.Y
	overChat := false
	if g.wide {
		overChat = x >= g.listW+4
	} else {
		// Title, sub-tabs and legend, then the list panel with its borders.
		overChat = y >= 3+g.listRows+2
	}
	if overChat {
		m.scrollThread(dir * wheelLines)
		return m
	}
	m.focus = focusFeed
	m.move(dir)
	return m
}

// threadView is the open conversation as the screen draws it: every line,
// the rows the pane has, and the first line shown now.
func (m Model) threadView() (lines []string, rows int, top int, id string) {
	convs := m.messageConversations()
	selected := m.selectedConversationIndex(convs)
	g := messagesLayout(max(m.width, 40), m.messagesBodyHeight())
	chat, chatSelected := m.messageThread(convs, selected, g.chatW)
	if len(convs) > 0 {
		id = convs[selected].id
	}
	rows = g.chatRows
	top = m.threadTopFor(chat, rows, chatSelected, id)
	return chat, rows, top, id
}

// scrollThread moves the open conversation by delta lines. A delta of 1 or -1
// is a page, less one line so the reader keeps a line of context. It stops at the oldest line, and at the newest line it goes
// back to following the conversation.
func (m *Model) scrollThread(delta int) {
	chat, rows, top, id := m.threadView()
	if id == "" {
		return
	}
	if delta == 1 || delta == -1 {
		delta *= max(rows-1, 1)
	}
	maxTop := max(len(chat)-rows, 0)
	top = min(max(top+delta, 0), maxTop)
	if delta > 0 && top >= maxTop {
		m.threadScrolled = false
		m.markLatestMessage()
		return
	}
	m.threadScrolled, m.threadTop, m.threadFor = true, top, id
}

// markLatestMessage selects the newest message of the open conversation.
func (m *Model) markLatestMessage() {
	rows := m.messageConversations()
	if len(rows) == 0 {
		return
	}
	row := rows[m.selectedConversationIndex(rows)]
	if len(row.items) > 0 {
		m.feedCur = row.items[len(row.items)-1]
	}
}
