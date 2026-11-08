package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

// Standing orders: the owner's lasting instructions to the CTO. The gateway
// keeps them. The CTO reads them at the start of every conversation. Section 6
// of Settings lists them: enter adds one, x removes the selected one and u
// puts the last removed one back.

type standingMsg struct {
	orders []protocol.StandingOrder
	loaded bool
	notice string
	err    error
	// removed is the order a remove call deleted, kept for u (undo).
	removed *protocol.StandingOrder
}

func (m Model) standingItems() []settingsItem {
	items := []settingsItem{{6, "standing-add", 0, "Add a standing order"}}
	for i, o := range m.standing {
		who := "you"
		if o.Source == protocol.StandingSourceCTO {
			who = "CTO"
		}
		when := time.Unix(o.SavedAt, 0).Format("2006-01-02")
		items = append(items, settingsItem{6, "standing", i, fmt.Sprintf("%s  (%s, %s)", sanitize(o.Text), who, when)})
	}
	return items
}

func (m Model) loadStandingOrders() tea.Cmd {
	return m.standingCall(protocol.StandingOrdersRequest{Action: protocol.StandingActionList}, "")
}

func (m Model) standingCall(q protocol.StandingOrdersRequest, notice string) tea.Cmd {
	src, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return standingMsg{err: fmt.Errorf("unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := src.Call(ctx, protocol.OpStandingOrders, q)
		if err != nil {
			return standingMsg{err: err}
		}
		var res protocol.StandingOrdersResult
		if err = json.Unmarshal(raw, &res); err != nil {
			return standingMsg{err: err}
		}
		out := standingMsg{orders: res.Orders, loaded: true, notice: notice}
		if q.Action == protocol.StandingActionRemove {
			out.removed = res.Order
		}
		return out
	}
}

func (m Model) onStanding(msg standingMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fail("Standing orders: " + explain(msg.err))
		return m, nil
	}
	if msg.loaded {
		m.standing = msg.orders
	}
	if msg.removed != nil {
		m.standingRemoved = msg.removed
	}
	if msg.notice != "" {
		m.say(msg.notice)
	}
	items := m.settingsItems()
	m.settingsRow = min(m.settingsRow, len(items)-1)
	return m, nil
}

func (m Model) startStandingAdd() (Model, tea.Cmd) {
	m.mode = modeStandingAdd
	m.rename = newTextInput()
	m.rename.Prompt = "standing order> "
	m.rename.SetVirtualCursor(false)
	m.notice = notice{}
	return m, m.rename.Focus()
}

func (m Model) standingAddKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeNormal
		m.say("No standing order added.")
		return m, nil
	case "submit":
		text, err := protocol.CleanStandingText(m.rename.Value())
		if err != nil {
			m.fail(err.Error())
			return m, nil
		}
		m.mode = modeNormal
		return m, m.standingCall(protocol.StandingOrdersRequest{Action: protocol.StandingActionSave, Text: text}, "Standing order saved. The CTO reads it in its next conversation.")
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}

// standingKey handles x (remove) and u (undo the last removal) in the
// standing orders section.
func (m Model) standingKey(action string, item settingsItem) (tea.Model, tea.Cmd) {
	switch action {
	case "revoke":
		if item.kind != "standing" || item.index >= len(m.standing) {
			return m, nil
		}
		return m, m.standingCall(protocol.StandingOrdersRequest{Action: protocol.StandingActionRemove, ID: m.standing[item.index].ID}, "Standing order removed. Press u to put it back.")
	case "url":
		if m.standingRemoved == nil {
			m.fail("No removed standing order to put back.")
			return m, nil
		}
		back := m.standingRemoved
		m.standingRemoved = nil
		return m, m.standingCall(protocol.StandingOrdersRequest{Restore: back}, "Standing order put back.")
	}
	return m, nil
}
