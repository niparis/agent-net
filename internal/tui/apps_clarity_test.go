package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The owner names a bot with "n" in Connected apps, from Settings and from
// the full screen. The rename field opens with the current name.
func TestConnectedAppNameKey(t *testing.T) {
	f := &connectedAppsFake{result: `{}`}
	m := newModel(120, 34)
	m.src = f
	m.tab = settingsTab
	m.apps.apps = []connectedApp{{ID: "a1", Name: "cursor", Kind: "bot", ClientName: "Cursor"}}
	items := m.settingsItems()
	for i, item := range items {
		if item.kind == "app" {
			m.settingsRow = i
			break
		}
	}
	m, _ = press(m, tea.KeyPressMsg{Text: "n", Code: 'n'})
	if m.apps.stage != "rename" || m.apps.input.Value() != "cursor" {
		t.Fatalf("settings n: stage %q value %q", m.apps.stage, m.apps.input.Value())
	}
	m.apps.stage = ""
	m.apps.input.SetValue("")
	m.mode = modeApps
	m.tab = settingsTab
	m, _ = press(m, tea.KeyPressMsg{Text: "n", Code: 'n'})
	if m.apps.stage != "rename" || m.apps.input.Value() != "cursor" {
		t.Fatalf("apps n: stage %q value %q", m.apps.stage, m.apps.input.Value())
	}
	m.apps.input.SetValue("Trade")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.rename" || f.params["id"] != "a1" || f.params["name"] != "Trade" {
		t.Fatalf("rename call: %s %+v", f.op, f.params)
	}
}

// The old tab binding is gone: Tab never did anything in the list.
func TestConnectedAppsHelpShowsNameKey(t *testing.T) {
	m := newModel(120, 34)
	m.mode = modeApps
	help := strings.Join(m.helpContent(120), "\n")
	if !strings.Contains(help, "n") || !strings.Contains(help, "name") || strings.Contains(help, "edit name") {
		t.Fatalf("help does not show the name key:\n%s", help)
	}
	m.tab = settingsTab
	m.mode = modeNormal
	m.apps.apps = []connectedApp{{ID: "a1", Name: "cursor", Kind: "bot"}}
	for i, item := range m.settingsItems() {
		if item.kind == "app" {
			m.settingsRow = i
			break
		}
	}
	var parts []string
	for _, h := range m.hints() {
		parts = append(parts, h.key+" "+h.label)
	}
	hint := strings.Join(parts, "  ")
	if !strings.Contains(hint, "n name") {
		t.Fatalf("settings hint on an app row has no name key: %q", hint)
	}
}

// The line under a row says which servers the bot can call, by name only.
func TestConnectedAppCanCallLine(t *testing.T) {
	m := newModel(120, 34)
	m.mode = modeApps
	m.apps.apps = []connectedApp{
		{ID: "a", Name: "trade", Kind: "bot", Grants: []string{"mcp server tenebrae-library", "mcp server ingest-forge"}},
		{ID: "b", Name: "grok-bot", Kind: "bot"},
		{ID: "p", Name: "phone", Kind: "owner_app"},
	}
	view := ansi.Strip(strings.Join(m.appsView(120, 30), "\n"))
	for _, want := range []string{"can call: ingest-forge, tenebrae-library", "can call: agent-net tools only"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "mcp server") || strings.Contains(view, "grants:") {
		t.Errorf("internal grant text shown:\n%s", view)
	}
	if strings.Count(view, "can call:") != 2 {
		t.Errorf("owner app should have no can-call line:\n%s", view)
	}
}

// The picker names the bot and its servers, with the host and the tools the
// bot may call through each one.
func TestConnectedAppGrantPickerLabels(t *testing.T) {
	f := &connectedAppsFake{result: `{"servers":[` +
		`{"name":"ingest-forge","hostname":"research","tools":[{"name":"search"},{"name":"hidden"}],"allowed_tools":["search"]},` +
		`{"name":"tenebrae-library","hostname":"research","tools":[{"name":"read"},{"name":"list"}]},` +
		`{"name":"notes","hostname":"nas"}]}`}
	m := newModel(120, 34)
	m.src = f
	m.mode = modeApps
	m.apps.apps = []connectedApp{{ID: "a1", Name: "Trade", Kind: "bot", Grants: []string{"mcp server ingest-forge"}}}
	m, cmd := press(m, tea.KeyPressMsg{Text: "g", Code: 'g'})
	m = runAppsCmd(t, m, cmd)
	view := ansi.Strip(strings.Join(m.appsView(120, 20), "\n"))
	for _, want := range []string{
		"MCP servers Trade can call. Enter adds or removes, esc closes.",
		"[x] ingest-forge  on research · 1 tool",
		"[ ] tenebrae-library  on research · 2 tools",
		"[ ] notes  on nas",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Shared MCP servers") || strings.Contains(view, "on nas ·") {
		t.Errorf("old picker text or a count without tools:\n%s", view)
	}
}

// A grant for a server that is gone says so, instead of "on removed".
func TestConnectedAppGrantGoneServerLabel(t *testing.T) {
	m := newModel(120, 34)
	m.mode = modeApps
	m.apps.apps = []connectedApp{{ID: "a1", Name: "trade", Kind: "bot", Grants: []string{"mcp server gone"}}}
	m.apps.stage = "grant"
	m, _ = m.onAppsResult(appsResult{op: "tools_mcp_list", raw: []byte(`{"servers":[]}`)})
	view := ansi.Strip(strings.Join(m.appsView(120, 20), "\n"))
	if !strings.Contains(view, "[x] gone  no longer added") || strings.Contains(view, "on removed") {
		t.Errorf("gone server label:\n%s", view)
	}
}

// The LAST USED time is never cut at 120 columns, and the date columns keep
// their width at 80 columns.
func TestSettingsAppRowKeepsDates(t *testing.T) {
	fields := []string{"cursor", "bot", "Cursor", "www.cursor.com", "Nov 02", "today 22:14"}
	for _, w := range []int{120, 80} {
		row := settingsAppRow(w, fields)
		if !strings.Contains(row, "today 22:14") || !strings.Contains(row, "Nov 02") {
			t.Errorf("width %d: dates cut: %q", w, row)
		}
		if ansi.StringWidth(row)+2 > w {
			t.Errorf("width %d: row is %d wide", w, ansi.StringWidth(row))
		}
	}
}

// The Tools tab shows which bots an MCP server is granted to.
func TestToolsMCPServerShowsBots(t *testing.T) {
	m, _ := toolsAtScreen(120, 34)
	m = rowByName(m, "tenebrae-library")
	revoked := m.now()
	m.tl.bots = botsByServer([]connectedApp{
		{Name: "Trade", Kind: "bot", Grants: []string{"mcp server tenebrae-library"}},
		{Name: "Grok Bot", Kind: "bot", Grants: []string{"mcp server tenebrae-library", "mcp server notes"}},
		{Name: "phone", Kind: "owner_app", Grants: []string{"mcp server tenebrae-library"}},
		{Name: "old", Kind: "bot", Grants: []string{"mcp server tenebrae-library"}, RevokedAt: &revoked},
	})
	detail := ansi.Strip(strings.Join(m.toolDetailView(60, 12), "\n"))
	if !strings.Contains(detail, "bots: Trade, Grok Bot") {
		t.Fatalf("bots line missing:\n%s", detail)
	}
	if strings.Contains(detail, "phone") || strings.Contains(detail, "old") {
		t.Fatalf("owner app or revoked app listed:\n%s", detail)
	}
	m.tl.bots = nil
	detail = ansi.Strip(strings.Join(m.toolDetailView(60, 12), "\n"))
	if strings.Contains(detail, "bots:") {
		t.Fatalf("bots line without grants:\n%s", detail)
	}
}
