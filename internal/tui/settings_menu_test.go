package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// openSessionSetting opens the S menu and chooses one of its four entries.
func openSessionSetting(m Model, index int) (Model, tea.Cmd) {
	m = keys(m, "S")
	for i := 0; i < index; i++ {
		m = keys(m, "down")
	}
	return press(m, tea.KeyMsg{Type: tea.KeyEnter})
}

func TestSessionSettingsMenuHasFourEntries(t *testing.T) {
	m := keys(newModel(120, 34), "S")
	if !m.settingsMenuOn {
		t.Fatal("S did not open the settings menu")
	}
	view := ansi.Strip(strings.Join(m.settingsMenuView(120), "\n"))
	for _, want := range []string{"Session settings", "request rule", "delegation policy", "integrations", "name this machine"} {
		if !strings.Contains(view, want) {
			t.Errorf("menu lacks %q:\n%s", want, view)
		}
	}
	if line := ansi.Strip(m.helpLine(120)); !strings.Contains(line, "enter choose") || !strings.Contains(line, "esc close") {
		t.Errorf("key line does not describe the menu: %q", line)
	}
	m = keys(m, "esc")
	if m.settingsMenuOn {
		t.Fatal("esc did not close the settings menu")
	}
}

func TestSessionSettingsMenuOpensEachForm(t *testing.T) {
	for _, tc := range []struct {
		index int
		name  string
		want  inputMode
		cmd   bool
	}{
		{0, "request rule", modeNormal, true},
		{1, "delegation policy", modePolicy, false},
		{2, "integrations", modeIntegrations, true},
		{3, "name this machine", modeRename, false},
	} {
		base := newModel(120, 34)
		if tc.index == 0 {
			base.src = &ruleSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
		}
		m, cmd := openSessionSetting(base, tc.index)
		if m.settingsMenuOn {
			t.Errorf("%s: the menu stayed open", tc.name)
		}
		if tc.cmd && cmd == nil {
			t.Errorf("%s: no action started", tc.name)
		}
		if !tc.cmd && m.mode != tc.want {
			t.Errorf("%s: mode %v, want %v", tc.name, m.mode, tc.want)
		}
	}
}
