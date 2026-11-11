package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"
)

// cycleSessionView moves between the two session views with one key: active,
// then all with the offline sessions.
func (m *Model) cycleSessionView() {
	m.activeOnly = !m.activeOnly
	m.showOffline = !m.activeOnly
}

// cycleMessageFilter moves through the three Messages sub-tabs with one key.
// It keeps the old flags in step so the rest of the screen and the help text
// still describe the same view.
func (m Model) cycleMessageFilter() (Model, tea.Cmd) {
	next := messageAgents
	switch m.messageSubtab {
	case messageAgents:
		next = messageWorkers
	case messageWorkers:
		next = messageSystem
	}
	return m.setMessageSubtab(next)
}

// setMessageSubtab switches the Messages view and rebuilds the list from the
// rows already held.
func (m Model) setMessageSubtab(s messageSubtab) (Model, tea.Cmd) {
	m.messageSubtab = s
	m.convSel = 0
	m.convAutoPages = 0
	switch s {
	case messageWorkers:
		m.hideWorkerThreads, m.messageShowEvents = true, false
	case messageSystem:
		m.hideWorkerThreads, m.messageShowEvents = false, true
	default:
		m.hideWorkerThreads, m.messageShowEvents = false, false
	}
	if m.convLoaded {
		m.rebuildConversations()
		return m, nil
	}
	if rows := m.messageConversations(); len(rows) > 0 && len(rows[0].items) > 0 {
		m.feedCur = rows[0].items[0]
	}
	return m, nil
}

// cycleThreadProject moves the project filter through the projects the held
// threads name, then back to all.
func (m Model) cycleThreadProject() (Model, tea.Cmd) {
	values := m.threadFilterValues(true)
	m.threadProject = nextFilterValue(values, m.threadProject)
	if m.convLoaded {
		m.rebuildConversations()
	}
	return m, nil
}

// cycleThreadTeam moves the team filter the same way.
func (m Model) cycleThreadTeam() (Model, tea.Cmd) {
	values := m.threadFilterValues(false)
	m.threadTeam = nextFilterValue(values, m.threadTeam)
	if m.convLoaded {
		m.rebuildConversations()
	}
	return m, nil
}

// threadFilterValues lists the projects or teams the loaded rows name, sorted.
func (m Model) threadFilterValues(project bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	rows := m.messageConversations()
	for _, r := range rows {
		for _, id := range []string{r.from, r.to} {
			if project {
				add(m.sessionProject(id))
			} else {
				add(m.sessionTeam(id))
			}
		}
	}
	sort.Strings(out)
	return out
}

// nextFilterValue cycles all -> first -> ... -> all.
func nextFilterValue(values []string, current string) string {
	if current == "" {
		if len(values) > 0 {
			return values[0]
		}
		return ""
	}
	for i, v := range values {
		if v == current {
			if i+1 < len(values) {
				return values[i+1]
			}
			return ""
		}
	}
	return ""
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
