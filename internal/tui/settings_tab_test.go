package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/funnel"
)

func TestSettingsTabAndRemovedKeys(t *testing.T) {
	m := newModel(120, 34)
	for _, key := range []string{"S", "t", "p"} {
		n := keys(m, key)
		if n.mode != modeNormal {
			t.Errorf("%s still acts", key)
		}
	}
	m = keys(m, "6")
	if m.tab != 5 {
		t.Fatalf("6: tab = %d", m.tab)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"Connected apps", "Integrations", "Machines", "Notifications", "Tips"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings lacks %q", want)
		}
	}
}

func TestSettingsSessionCells(t *testing.T) {
	src := &ruleSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
	m := newModel(120, 34)
	m.src = src
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := ansi.Strip(m.helpLine(120)); !strings.Contains(got, "space") || !strings.Contains(got, "ask") {
		t.Errorf("rule hint: %s", got)
	}
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeySpace})
	if cmd == nil {
		t.Fatal("space on RULE did not call the request rule operation")
	}
	cmd()
	if len(src.edges) != 1 || src.edges[0].Rule != "ask" {
		t.Errorf("rule calls: %+v", src.edges)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modePolicy {
		t.Fatalf("enter on DELEGATE: mode %v", m.mode)
	}
	m = keys(m, "esc", "esc")
	if got := ansi.Strip(m.helpLine(120)); strings.Contains(got, "space") {
		t.Errorf("esc did not return to NAME: %s", got)
	}
}

func TestSettingsOtherRoutesAndViewIndicators(t *testing.T) {
	m := newModel(120, 34)
	m = keys(m, "6")
	result := appsResult{op: "apps.public.status", raw: json.RawMessage(`{"on":true,"url":"https://example.test","other_routes":[8443,10000]}`)}
	m, _ = m.onAppsResult(result)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "8443") || !strings.Contains(strings.Join(strings.Fields(view), " "), "sign-in cookie") {
		t.Errorf("missing other routes warning:\n%s", view)
	}
	s := newModel(120, 34)
	title := ansi.Strip(s.titleLine(120))
	s = keys(s, "v")
	if got := ansi.Strip(s.titleLine(120)); got != title {
		t.Errorf("session view changed tab bar: %q != %q", got, title)
	}
	if !strings.Contains(ansi.Strip(strings.Join(s.sessionsTable(120, 10), "\n")), "view:") {
		t.Error("missing session view indicator")
	}
	s = keys(s, "4")
	title = ansi.Strip(s.titleLine(120))
	s = keys(s, "l")
	if got := ansi.Strip(s.titleLine(120)); got != title {
		t.Errorf("message filter changed tab bar: %q != %q", got, title)
	}
}

func selectSettingsKind(m Model, kind string) Model {
	for i, row := range m.settingsItems() {
		if row.kind == kind {
			m.settingsRow = i
			return m
		}
	}
	return m
}

func TestSettingsActionsUseExistingOperations(t *testing.T) {
	f := &connectedAppsFake{result: `{"apps":[]}`}
	m := newModel(120, 34)
	m.src = f
	m, cmd := press(m, keyRunes("6"))
	if m.tab != settingsTab || cmd == nil {
		t.Fatal("opening Settings did not load apps")
	}
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.list" {
		t.Fatalf("load: %s", f.op)
	}
	m.apps.loading = false
	m = selectSettingsKind(m, "public")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeySpace})
	if cmd == nil {
		t.Fatal("public toggle")
	}
	cmd()
	if f.op != "apps.public.on" {
		t.Fatal(f.op)
	}
	m.apps.loading = false
	m.apps.public.Error = funnel.ErrNotInstalled.Error()
	m = selectSettingsKind(m, "url")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeApps || m.apps.stage != "url" {
		t.Fatal("URL editor")
	}
	m = keys(m, "esc")
	m.apps.public.On = true
	m = selectSettingsKind(m, "add")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.apps.stage != "code" {
		t.Fatal("add flow")
	}
	m = keys(m, "esc")
	m.apps.apps = []connectedApp{{ID: "app-1", Name: "client"}}
	m = selectSettingsKind(m, "app")
	m, _ = press(m, keyRunes("x"))
	if m.apps.stage != "confirm" {
		t.Fatal("revoke confirmation")
	}
	m, cmd = press(m, keyRunes("y"))
	if cmd == nil {
		t.Fatal("revoke command")
	}
	cmd()
	if f.op != "apps.revoke" || f.params["id"] != "app-1" {
		t.Fatalf("revoke %s %+v", f.op, f.params)
	}

	base, src := workerModel(120, 34)
	base.tab = settingsTab
	base = selectSettingsKind(base, "integrations")
	base, cmd = press(base, tea.KeyMsg{Type: tea.KeyEnter})
	if base.mode != modeIntegrations || cmd == nil {
		t.Fatal("integrations form")
	}
	base.mode = modeNormal
	base = selectSettingsKind(base, "machine")
	machine := base.machineRows()[base.settingsItems()[base.settingsRow].index]
	base, cmd = press(base, tea.KeyMsg{Type: tea.KeyEnter})
	if base.mode != modeRename || base.renameNode != machine.nodeID {
		t.Fatal("machine rename")
	}
	base.rename.SetValue("desk")
	base, cmd = press(base, tea.KeyMsg{Type: tea.KeyEnter})
	_ = finish(t, base, cmd)
	if len(src.renames) != 1 || src.renames[0].id != machine.nodeID {
		t.Fatalf("rename %+v", src.renames)
	}
}

func TestSettingsSectionNavigationAndHelp(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	for _, want := range []string{"integrations", "machine", "notifications", "tips", "public"} {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
		if got := m.settingsItems()[m.settingsRow].kind; got != want {
			t.Fatalf("tab: got %s want %s", got, want)
		}
	}
	m, _ = press(m, keyRunes("["))
	if got := m.settingsItems()[m.settingsRow].kind; got != "tips" {
		t.Fatalf("previous section: %s", got)
	}
	m.help = true
	for _, key := range []string{"N", "T", "R"} {
		if _, cmd := press(m, keyRunes(key)); cmd != nil {
			t.Errorf("help key %s acted", key)
		}
	}
	help := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, want := range []string{"Settings", "move between NAME, RULE and DELEGATE", "cycle the request rule", "desktop notifications", "restart tips"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestSettingsGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		w, h := size[0], size[1]
		m := newModel(w, h)
		m.tab = settingsTab
		m.apps.public.On = true
		m.apps.public.Port = 8443
		m.apps.public.URL = "https://example.test:8443"
		m.apps.public.OtherRoutes = json.RawMessage(`[10000]`)
		m.apps.apps = []connectedApp{{ID: "a1", Name: "phone", Kind: "owner_app", ClientName: "Safari"}, {ID: "a2", Name: "helper", Kind: "bot", ClientName: "Cursor"}}
		golden(t, fmt.Sprintf("settings_%dx%d", w, h), m.View())
		s := newModel(w, h)
		s, _ = press(s, tea.KeyMsg{Type: tea.KeyRight})
		golden(t, fmt.Sprintf("sessions_cells_%dx%d", w, h), s.View())
		s.activeOnly = true
		golden(t, fmt.Sprintf("sessions_active_%dx%d", w, h), s.View())
	}
}
