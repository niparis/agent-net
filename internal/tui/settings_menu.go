package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Sessions settings menu: one key opens the four settings that used to
// have a key each.
type settingsEntry struct {
	action actionID
	label  string
	help   string
}

func settingsEntries() []settingsEntry {
	return []settingsEntry{
		{"request rule", "request rule", "cycle the request rule: normal, ask, auto"},
		{"policy", "delegation policy", "subagent or worker for code changes (a form)"},
		{"integrations", "integrations", fmt.Sprintf("integrations for all machines: %d checkboxes (a form)", len(protocol.IntegrationSpecs()))},
		{"rename machine", "name this machine", "every machine sees it; empty resets"},
		{"connected apps", "Connected apps", "manage connected apps"},
	}
}

func (m *Model) openSettingsMenu() {
	m.settingsMenuOn = true
	m.settingsMenu = 0
	m.notice = notice{}
}

func (m Model) settingsMenuKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := settingsEntries()
	switch keyAction(contextSettingsMenu, k.String()) {
	case actClose:
		m.settingsMenuOn = false
	case actUp:
		m.settingsMenu = (m.settingsMenu + len(entries) - 1) % len(entries)
	case actDown:
		m.settingsMenu = (m.settingsMenu + 1) % len(entries)
	case "choose":
		m.settingsMenuOn = false
		switch entries[m.settingsMenu].action {
		case "request rule":
			return m.startTrust()
		case "policy":
			return m.startPolicy()
		case "integrations":
			return m.startIntegrations()
		case "rename machine":
			return m.startRename()
		case "connected apps":
			return m.startApps()
		}
	}
	return m, nil
}

// settingsMenuView draws the small menu under the machines.
func (m Model) settingsMenuView(w int) []string {
	entries := settingsEntries()
	var body []string
	for i, entry := range entries {
		mark := "  "
		label := cell(entry.label, 17)
		if i == m.settingsMenu {
			mark = "› "
			label = sSel.Render(label)
		}
		body = append(body, mark+label+" "+sDim.Render(entry.help))
	}
	return panel("Session settings", body, w, roleAccent)
}
