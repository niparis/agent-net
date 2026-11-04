package tui

import (
	"charm.land/lipgloss/v2"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestV2ViewDeclaresTerminalModes(t *testing.T) {
	m := graphModel(80, 24)
	v := m.View()
	if !v.AltScreen || v.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("graph terminal modes: %+v", v)
	}
	m.mode = modeSessionForm
	if v = m.View(); v.MouseMode != tea.MouseModeNone {
		t.Fatalf("form should release mouse: %+v", v)
	}
	m.quitting = true
	if v = m.View(); v.MouseMode != tea.MouseModeNone {
		t.Fatalf("quit should release mouse: %+v", v)
	}
}

func TestV2PasteIntoComposeDoesNotTriggerShortcuts(t *testing.T) {
	m := newModel(80, 24)
	m = selectID(m, workerID)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = next.(Model)
	if m.mode != modeCompose {
		t.Fatal("compose did not open")
	}
	next, _ = m.Update(tea.PasteMsg{Content: "quit x"})
	m = next.(Model)
	if m.quitting || m.compose.Value() != "quit x" {
		t.Fatalf("paste: quitting=%t value=%q", m.quitting, m.compose.Value())
	}
}

func TestV2PasteIntoSessionFormAndFilters(t *testing.T) {
	m := newModel(80, 24)
	m.mode = modeSessionForm
	m.sform = newSessionForm()
	next, _ := m.Update(tea.PasteMsg{Content: "q x"})
	m = next.(Model)
	if got := m.sform.in[sFirstMessage].Value(); got != "q x" || m.quitting {
		t.Fatalf("session form paste: %q, quitting %v", got, m.quitting)
	}
	m.mode = modeNormal
	m.filtering = true
	m.input.Focus()
	next, _ = m.Update(tea.PasteMsg{Content: "q x"})
	m = next.(Model)
	if m.filter != "q x" || m.quitting {
		t.Fatalf("filter paste: %q", m.filter)
	}
}

func TestV2TextInputSpaceAndShiftTab(t *testing.T) {
	m := newModel(80, 24)
	m.mode = modeSessionForm
	m.sform = newSessionForm()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = next.(Model)
	if got := m.sform.in[sFirstMessage].Value(); got != " " {
		t.Fatalf("space: %q", got)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = next.(Model)
	if m.sform.cur == sFirstMessage {
		t.Fatal("shift+tab did not move to the previous field")
	}
}

func TestV2CtrlHDeletesFromToolsFilters(t *testing.T) {
	key := tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl}
	m := newModel(80, 24)
	m.tl.searching, m.tl.search = true, "abc"
	next, _ := m.toolsSearchKey(key)
	m = next.(Model)
	if m.tl.search != "ab" {
		t.Fatalf("tools search: %q", m.tl.search)
	}
	m.tl.agentSearching, m.tl.agentFilter = true, "abc"
	next, _ = m.agentToolsKey(key)
	m = next.(Model)
	if m.tl.agentFilter != "ab" {
		t.Fatalf("skills filter: %q", m.tl.agentFilter)
	}
	m.tl.modal = &toolsModal{picker: &projPicker{filtering: true, filter: "abc"}}
	next, _ = m.pickerKey(key)
	m = next.(Model)
	if m.tl.modal.picker.filter != "ab" {
		t.Fatalf("picker filter: %q", m.tl.modal.picker.filter)
	}
}

func TestV2InputPromptKeepsTerminalDefaultColor(t *testing.T) {
	m := selectID(newModel(80, 24), workerID)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = next.(Model)
	if got := m.compose.View(); !strings.HasPrefix(got, "to ") {
		t.Fatalf("compose prompt should use the terminal's default color: %q", got)
	}
}

func TestV2SemanticColorsMatchOriginalTrueColor(t *testing.T) {
	cases := []struct {
		name string
		col  color.Color
		want string
	}{
		{"accent", roleAccent, "38;2;95;195;201"},
		{"warning", roleWarning, "38;2;240;179;89"},
		{"CTO", roleCTO, "38;2;179;156;245"},
		{"mature", roleMature, "38;2;121;167;245"},
	}
	for _, tc := range cases {
		if got := lipgloss.NewStyle().Foreground(tc.col).Render("x"); !strings.Contains(got, tc.want) {
			t.Errorf("%s color: %q, want %s", tc.name, got, tc.want)
		}
	}
}
