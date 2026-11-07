package tui

import tea "charm.land/bubbletea/v2"

// cycleSessionView moves between the two session views with one key: active,
// then all with the offline sessions.
func (m *Model) cycleSessionView() {
	m.activeOnly = !m.activeOnly
	m.showOffline = !m.activeOnly
}

// cycleMessageFilter moves through the three Messages filters with one key.
func (m Model) cycleMessageFilter() (Model, tea.Cmd) {
	switch {
	case m.messageShowEvents:
		// with system events -> all
		m.messageShowEvents, m.hideWorkerThreads = false, false
	case m.hideWorkerThreads:
		// without worker threads -> with system events
		m.hideWorkerThreads, m.messageShowEvents = false, true
	default:
		// all -> without worker threads
		m.hideWorkerThreads, m.messageShowEvents = true, false
	}
	if m.convLoaded {
		m.rebuildConversations()
		m.convSel = min(m.convSel, max(len(m.convs)-1, 0))
		return m, nil
	}
	rows := m.messageConversations()
	selected := false
	for _, row := range rows {
		for _, i := range row.items {
			if i == m.feedCur {
				selected = true
			}
		}
	}
	if !selected && len(rows) > 0 {
		m.feedCur = rows[0].items[0]
	}
	return m, nil
}

// messageFilterName is the filter the l key selected.
func (m Model) messageFilterName() string {
	switch {
	case m.messageShowEvents:
		return "with system events"
	case m.hideWorkerThreads:
		return "without worker threads"
	}
	return "all"
}
