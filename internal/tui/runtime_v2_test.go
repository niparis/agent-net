package tui

import (
	"bytes"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/niparis/agent-net/internal/tui/palette"
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

func TestV2ColorProfilesPreserveOriginalPalette(t *testing.T) {
	defer palette.SetProfile(colorprofile.TrueColor)
	for _, tc := range []struct {
		profile colorprofile.Profile
		want    string
	}{
		{colorprofile.ANSI256, "38;5;59"},
		{colorprofile.ANSI, "90m"},
	} {
		m := newModel(80, 24)
		_, _ = m.Update(tea.ColorProfileMsg{Profile: tc.profile})
		var output bytes.Buffer
		writer := colorprofile.NewWriter(&output, nil)
		writer.Profile = tc.profile
		_, _ = writer.WriteString(lipgloss.NewStyle().Foreground(roleFaint).Render("x"))
		if !strings.Contains(output.String(), tc.want) {
			t.Errorf("%v: got %q, want %s", tc.profile, output.String(), tc.want)
		}
	}
}

func TestV2PasteUsesOnlyTheActiveEditor(t *testing.T) {
	m := newModel(80, 24)
	m.tab = settingsTab
	m.apps.stage = "rename"
	m.apps.input = newTextInput()
	m.apps.input.Focus()
	next, _ := m.Update(tea.PasteMsg{Content: "new name"})
	m = next.(Model)
	if got := m.apps.input.Value(); got != "new name" {
		t.Errorf("settings rename paste: %q", got)
	}

	m = newModel(80, 24)
	m.focus = focusCTO
	m.filtering = true
	m.input.Focus()
	next, _ = m.Update(tea.PasteMsg{Content: "hidden"})
	m = next.(Model)
	if m.filter != "" {
		t.Errorf("paste changed hidden filter: %q", m.filter)
	}

	m = newModel(80, 24)
	m.tab = toolsTab
	m.tl.searching, m.tl.search = true, "keep"
	m.tl.modal = &toolsModal{}
	next, _ = m.Update(tea.PasteMsg{Content: "hidden"})
	m = next.(Model)
	if m.tl.search != "keep" {
		t.Errorf("paste changed filter behind modal: %q", m.tl.search)
	}
}

func TestV2PasteIntoToolsEditors(t *testing.T) {
	m := newModel(80, 24)
	m.tab, m.tl.allTools, m.tl.searching = toolsTab, true, true
	next, _ := m.Update(tea.PasteMsg{Content: "hello q"})
	m = next.(Model)
	if m.tl.search != "hello q" || m.quitting {
		t.Errorf("tools search: %q", m.tl.search)
	}

	m = newModel(80, 24)
	m.tab, m.tl.agentSearching = toolsTab, true
	next, _ = m.Update(tea.PasteMsg{Content: "skills"})
	m = next.(Model)
	if m.tl.agentFilter != "skills" {
		t.Errorf("skills search: %q", m.tl.agentFilter)
	}

	m.tl.modal = &toolsModal{picker: &projPicker{filtering: true}}
	next, _ = m.Update(tea.PasteMsg{Content: "project"})
	m = next.(Model)
	if m.tl.modal.picker.filter != "project" || m.tl.agentFilter != "skills" {
		t.Errorf("picker: %+v", m.tl.modal.picker)
	}

	m.tl.modal = &toolsModal{form: &toolAddForm{values: []string{""}}}
	next, _ = m.Update(tea.PasteMsg{Content: "a path"})
	m = next.(Model)
	if m.tl.modal.form.values[0] != "a path" {
		t.Errorf("form: %+v", m.tl.modal.form.values)
	}
}
