package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// sessionPinMsg is the answer of the owner-only session_pin call.
type sessionPinMsg struct {
	id     string
	pinned bool
	err    error
}

// sessionPinCall pins or unpins one session through the operator's owner-only
// call, the same way the archive dialog calls sessions_archive.
func (m Model) sessionPinCall(id string, pinned bool) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return sessionPinMsg{id: id, pinned: pinned, err: fmt.Errorf("session pin unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, "session_pin", map[string]any{"session_id": id, "pinned": pinned})
		return sessionPinMsg{id: id, pinned: pinned, err: err}
	}
}

func (m Model) toggleSessionPin() (tea.Model, tea.Cmd) {
	if m.tab != sessionsTab || m.focus != focusSessions || m.selected == "" {
		return m, nil
	}
	a, ok := m.selectedAgent()
	if !ok || protocol.IsOwner(a.Harness) || a.Harness == "bot" {
		return m, nil
	}
	return m, m.sessionPinCall(a.SessionID, a.PinnedAt == 0)
}

func (m Model) sessionPinResult(r sessionPinMsg) (tea.Model, tea.Cmd) {
	if r.err != nil {
		m.failErr(r.err, "Could not pin session: "+sanitize(r.err.Error()))
		return m, nil
	}
	name := r.id
	if a, ok := m.agentByID(r.id); ok {
		name = rowName(a)
	}
	// Move the row at once; the next refresh carries the gateway's own time.
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID != r.id {
			continue
		}
		if r.pinned {
			if m.snap.Agents[i].PinnedAt == 0 {
				m.snap.Agents[i].PinnedAt = m.now().Unix()
			}
		} else {
			m.snap.Agents[i].PinnedAt = 0
		}
	}
	if r.pinned {
		m.say("Pinned " + name + " at the top of Mine.")
	} else {
		m.say("Unpinned " + name + ".")
	}
	m.ensureSelection()
	m.fixScroll()
	return m, m.fetchCmd()
}
