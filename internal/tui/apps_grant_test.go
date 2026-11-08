package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The owner picks the shared MCP servers a bot may use from its row.
func TestConnectedAppGrantFlow(t *testing.T) {
	f := &connectedAppsFake{result: `{"apps":[]}`}
	m := newModel(120, 34)
	m.src = f
	m.mode = modeApps
	m.apps.apps = []connectedApp{{ID: "a1", Name: "trade-bot", Kind: "bot"}}
	var cmd tea.Cmd
	m, cmd = press(m, tea.KeyPressMsg{Text: "g", Code: 'g'})
	f.result = `{"servers":[{"name":"ingest-forge","hostname":"research"},{"name":"another","hostname":"nas"}]}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "tools_mcp_list" || m.apps.stage != "grant" || len(m.apps.servers) != 2 || m.apps.servers[0].name != "another" {
		t.Fatalf("picker: op %s %+v", f.op, m.apps)
	}
	view := ansi.Strip(strings.Join(m.appsView(120, 20), "\n"))
	for _, want := range []string{"trade-bot may use", "[ ] another", "[ ] ingest-forge", "on research"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker missing %q:\n%s", want, view)
		}
	}
	m = keys(m, "down")
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	f.result = `{}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.grant" || f.params["id"] != "a1" || f.params["grant"] != "mcp server ingest-forge" {
		t.Fatalf("grant call: %s %+v", f.op, f.params)
	}
	// The list is read again, with the grant on the row.
	if cmd == nil {
		t.Fatal("no command")
	}
	f.result = `{"apps":[{"ID":"a1","Name":"trade-bot","Kind":"bot","Grants":["mcp server ingest-forge"]}]}`
	m.apps.loading = false
	m, cmd = m.onAppsResult(appsResult{op: "apps.list", raw: []byte(f.result)})
	if m.apps.stage != "grant" {
		t.Fatalf("picker closed: %q", m.apps.stage)
	}
	view = ansi.Strip(strings.Join(m.appsView(120, 20), "\n"))
	if !strings.Contains(view, "[x] ingest-forge") {
		t.Errorf("grant not shown:\n%s", view)
	}
	// Enter on a granted server removes the grant.
	m.apps.loading = false
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	f.result = `{}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.ungrant" || f.params["grant"] != "mcp server ingest-forge" {
		t.Fatalf("ungrant call: %s %+v", f.op, f.params)
	}
	m.apps.loading = false
	m = keys(m, "esc")
	if m.apps.stage != "" {
		t.Fatal("esc did not close the picker")
	}
	m.apps.apps[0].Grants = []string{"mcp server ingest-forge"}
	if view = ansi.Strip(strings.Join(m.appsView(120, 20), "\n")); !strings.Contains(view, "grants: mcp server ingest-forge") {
		t.Errorf("grant not on the row:\n%s", view)
	}
}

func TestConnectedAppGrantOnlyForBots(t *testing.T) {
	f := &connectedAppsFake{result: `{}`}
	m := newModel(120, 34)
	m.src = f
	m.mode = modeApps
	m.apps.apps = []connectedApp{{ID: "p1", Name: "phone", Kind: "owner_app"}}
	m, cmd := press(m, tea.KeyPressMsg{Text: "g", Code: 'g'})
	if cmd != nil || m.apps.stage != "" || !strings.Contains(m.apps.err, "Only a bot") {
		t.Fatalf("owner app: stage %q err %q", m.apps.stage, m.apps.err)
	}
}

// A grant for a server that no longer exists stays in the picker, so it can be removed.
func TestConnectedAppGrantKeepsRemovedServer(t *testing.T) {
	m := newModel(120, 34)
	m.mode = modeApps
	m.apps.apps = []connectedApp{{ID: "a1", Name: "trade-bot", Kind: "bot", Grants: []string{"mcp server gone"}}}
	m.apps.stage = "grant"
	m, _ = m.onAppsResult(appsResult{op: "tools_mcp_list", raw: []byte(`{"servers":[]}`)})
	if len(m.apps.servers) != 1 || m.apps.servers[0].name != "gone" {
		t.Fatalf("servers %+v", m.apps.servers)
	}
}

func TestConnectedAppGrantSettingsGoldens(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(sz[0], sz[1])
		m.tab = settingsTab
		stamp := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		m.apps.apps = []connectedApp{
			{ID: "a", Name: "trade-bot", Kind: "bot", ClientName: "Trade", CreatedAt: stamp, Grants: []string{"mcp server ingest-forge"}},
			{ID: "b", Name: "grok-bot", Kind: "bot", ClientName: "Grok Bot", CreatedAt: stamp},
		}
		golden(t, fmt.Sprintf("connected_apps_grants_%d", sz[0]), m.testView())
		m.apps.stage = "grant"
		m.apps.servers = []grantServer{{"ingest-forge", "research"}, {"notes", "nas"}}
		golden(t, fmt.Sprintf("connected_apps_grant_picker_%d", sz[0]), m.testView())
	}
}
