package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestSettingsRouteWarningGrammar(t *testing.T) {
	for _, tc := range []struct{ routes, want string }{
		{`[10000]`, "(port 10000)"},
		{`[10000,443]`, "(ports 443, 10000)"},
	} {
		m := newModel(120, 34)
		m.tab = settingsTab
		m.apps.public.OtherRoutes = json.RawMessage(tc.routes)
		for _, view := range []string{strings.Join(m.settingsView(120, 50), "\n"), strings.Join(m.appsView(120, 50), "\n")} {
			if !strings.Contains(ansi.Strip(view), tc.want) {
				t.Errorf("routes %s: want %q in %s", tc.routes, tc.want, view)
			}
		}
	}
}

func TestSettingsTunnelRowAvailability(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status appsPublicStatus
		want   string
	}{
		{"off", appsPublicStatus{}, "Use your own tunnel URL"},
		{"funnel on", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{On: true, Port: 8443, URL: "https://node.example.ts.net:8443"}}, ""},
		{"funnel off", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{Port: 8443, URL: "https://node.example.ts.net:8443"}}, "Use your own tunnel URL"},
		{"not installed", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{Error: funnel.ErrNotInstalled.Error()}}, "Use your own tunnel URL"},
		{"not running", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{Error: funnel.ErrNotRunning.Error()}}, "Use your own tunnel URL"},
		{"manual on", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{On: true, URL: "https://my-tunnel.example"}}, "Tunnel URL: https://my-tunnel.example"},
		{"manual off", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{URL: "https://my-tunnel.example"}}, "Use your own tunnel URL"},
		{"funnel on with error", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{On: true, Port: 8443, URL: "https://node.example.ts.net:8443", Error: funnel.ErrNotRunning.Error()}}, ""},
		{"on with no URL", appsPublicStatus{AppsPublicStatus: protocol.AppsPublicStatus{On: true}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(120, 34)
			m.tab = settingsTab
			m.apps.public = tc.status
			got := ""
			for _, item := range m.settingsItems() {
				if item.kind == "url" {
					got = item.text
				}
			}
			if got != tc.want {
				t.Errorf("URL row %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSettingsColumnsAndSectionSpacing(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	m.apps.apps = []connectedApp{{ID: "a", Name: "phone", Kind: "owner_app", ClientName: "Safari"}}
	view := ansi.Strip(strings.Join(m.settingsView(120, 80), "\n"))
	if !strings.Contains(view, "owner app  Safari") {
		t.Errorf("app columns: %s", view)
	}
	for _, name := range []string{"Integrations", "Machines", "Notifications", "Tips"} {
		if !strings.Contains(view, "\n\n"+name+"\n") {
			t.Errorf("missing space before %s", name)
		}
	}
	machine := "  " + cellDots("Mac", 20) + "  " + cellDots("Mac", 20) + "  " + cellDots("v0.4.0", 12) + "  yes"
	if !strings.Contains(view, machine) {
		t.Errorf("machine columns: want %q", machine)
	}
	old := testColorProfile
	setTestColorProfile(termenv.TrueColor)
	colored := strings.Join(m.settingsView(120, 80), "\n")
	for _, name := range []string{"Connected apps", "Integrations", "Machines", "Notifications", "Tips"} {
		if !strings.Contains(colored, sBold.Render(name)) {
			t.Errorf("section title %q is not bold", name)
		}
	}
	setTestColorProfile(old)
	m.height = 24
	m.width = 80
	m = selectSettingsKind(m, "restart")
	if !strings.Contains(ansi.Strip(m.testView()), "▸ Restart tips") {
		t.Fatal("selected row scrolled out of view")
	}
}

func TestSessionSettingsColors(t *testing.T) {
	old := testColorProfile
	defer setTestColorProfile(old)
	setTestColorProfile(termenv.TrueColor)
	m := newModel(120, 34)
	a := m.snap.Agents[0]
	m.selected = "other"
	a.Online = true
	cols := []column{{name: "TRUST", width: 5}, {name: "RULE", width: 6}, {name: "DELEGATE", width: 8}}
	line := m.sessionLine(a, treeMark{}, cols, 120, 0)
	for _, want := range []string{sDim.Render(cell("-", 5)), sDim.Render(cell("normal", 6)), sDim.Render(cell("-", 8))} {
		if !strings.Contains(line, want) {
			t.Errorf("default style missing %q: %q", want, line)
		}
	}
	project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
	m.snap.Edges = []protocol.Edge{{From: "*", To: project, Rule: "ask"}}
	a.Delegation = &protocol.DelegationView{DelegationPolicy: protocol.DelegationPolicy{Delegate: protocol.DelegateNative}}
	line = m.sessionLine(a, treeMark{}, cols, 120, 0)
	for _, value := range []string{cell("ask", 5), cell("ask", 6), cell("native", 8)} {
		if !strings.Contains(line, value) || strings.Contains(line, sDim.Render(value)) {
			t.Errorf("non-default value should be normal text: %q in %q", value, line)
		}
	}
	m.selected = a.SessionID
	m.selIdx = 0
	m.sessionCell = 1
	line = m.sessionLine(a, treeMark{}, cols, 120, 0)
	if !strings.Contains(line, sSel.Render(cell("ask", 6))) {
		t.Errorf("selected rule is not highlighted: %q", line)
	}
	m.snap.Edges = nil
	line = m.sessionLine(a, treeMark{}, cols, 120, 0)
	if !strings.Contains(line, sSel.Render(cell("normal", 6))) {
		t.Errorf("default rule is not highlighted: %q", line)
	}
	for _, rule := range []string{"ask", "auto"} {
		m.selected = "other"
		m.snap.Edges = []protocol.Edge{{From: "*", To: project, Rule: rule}}
		line = m.sessionLine(a, treeMark{}, cols, 120, 0)
		if strings.Contains(line, sDim.Render(cell(rule, 6))) {
			t.Errorf("%s is dim: %q", rule, line)
		}
	}
}

func TestSettingsKeepsSelectedRowWhenTunnelAvailabilityChanges(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	m = selectSettingsKind(m, "add")
	m = send(m, appsResult{op: "apps.public.status", raw: json.RawMessage(`{"on":true,"port":8443,"url":"https://node.example.ts.net:8443"}`)})
	if got := m.settingsItems()[m.settingsRow].kind; got != "add" {
		t.Fatalf("after URL row disappeared: selected %q", got)
	}
	m = send(m, appsResult{op: "apps.public.status", raw: json.RawMessage(`{"on":false,"port":8443,"url":"https://node.example.ts.net:8443"}`)})
	if got := m.settingsItems()[m.settingsRow].kind; got != "add" {
		t.Fatalf("after URL row appeared: selected %q", got)
	}
}
