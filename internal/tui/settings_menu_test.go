package tui

import tea "github.com/charmbracelet/bubbletea"

// openSessionSetting keeps the older form tests focused on the same action.
func openSessionSetting(m Model, index int) (Model, tea.Cmd) {
	switch index {
	case 0:
		if _, ok := m.selectedAgent(); !ok {
			return m.startTrust()
		}
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		return press(m, tea.KeyMsg{Type: tea.KeySpace})
	case 1:
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		return press(m, tea.KeyMsg{Type: tea.KeyEnter})
	case 2:
		m.tab = settingsTab
		for i, row := range m.settingsItems() {
			if row.kind == "integrations" {
				m.settingsRow = i
				break
			}
		}
		return press(m, tea.KeyMsg{Type: tea.KeyEnter})
	case 3:
		a, _ := m.selectedAgent()
		m.tab = settingsTab
		for i, row := range m.settingsItems() {
			if row.kind == "machine" && m.machineRows()[row.index].nodeID == a.NodeID {
				m.settingsRow = i
				break
			}
		}
		return press(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	return m, nil
}
