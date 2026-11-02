package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestConnectedAppsSettingsEntry(t *testing.T) {
	m := keys(newModel(120, 34), "6")
	if !strings.Contains(m.View(), "Connected apps") {
		t.Fatal("Connected apps missing from settings")
	}
}

type connectedAppsFake struct {
	fakeSource
	op     string
	params map[string]string
	result string
	err    error
}

func (f *connectedAppsFake) Call(_ context.Context, op string, p any) (json.RawMessage, error) {
	f.op = op
	f.params, _ = p.(map[string]string)
	return json.RawMessage(f.result), f.err
}
func runAppsCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing command")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}
func TestConnectedAppsFlow(t *testing.T) {
	f := &connectedAppsFake{result: `{"apps":[]}`}
	m := newModel(120, 34)
	m.src = f
	var cmd tea.Cmd
	m, cmd = m.startApps()
	m = runAppsCmd(t, m, cmd)
	m.apps.public.On = true
	m = keys(m, "a")
	m.apps.input.SetValue("ABC-234")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	f.result = `{"pending":{"ID":"p1","ClientName":"Cursor","RedirectHost":"client.example"}}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.pending" || f.params["code"] != "ABC-234" || m.apps.input.Value() != "cursor" {
		t.Fatalf("lookup: %+v %+v", f, m.apps)
	}
	m = keys(m, "down", "right", "up")
	m.apps.input.SetValue("my app")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	f.result = `{}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.approve" || f.params["kind"] != "owner_app" || f.params["name"] != "my app" {
		t.Fatalf("approve: %+v", f)
	}
	m.apps.loading = false
	m.apps.stage = "approve"
	m.apps.editingName = false
	m.apps.pending.ID = "p2"
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.refuse" || f.params["id"] != "p2" {
		t.Fatalf("refuse: %+v", f)
	}
	m.apps.loading = false
	m.apps.stage = ""
	m.apps.apps = []connectedApp{{ID: "a1", Name: "bot"}}
	m = keys(m, "x")
	if m.apps.stage != "confirm" {
		t.Fatal("no confirmation")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.revoke" || f.params["id"] != "a1" {
		t.Fatalf("revoke: %+v", f)
	}
}
func TestConnectedAppsErrors(t *testing.T) {
	for _, tc := range []struct{ err, want string }{{"sql: no rows in result set", "No request with that code (expired or mistyped)"}, {"too many tries", "Too many wrong codes. Try again in 10 minutes."}} {
		m := newModel(80, 30)
		m.mode = modeApps
		m.apps.stage = "code"
		m, _ = m.onAppsResult(appsResult{op: "apps.pending", err: errors.New(tc.err)})
		if m.apps.err != tc.want {
			t.Fatalf("got %q", m.apps.err)
		}
	}
}

func TestConnectedAppsScreens(t *testing.T) {
	for _, sz := range [][2]int{{80, 30}, {120, 34}} {
		m := newModel(sz[0], sz[1])
		m.mode = modeApps
		stamp := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		used := time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)
		name := func(s string) string { return fmt.Sprintf("connected_apps_%s_%d", s, sz[0]) }
		golden(t, name("empty"), m.View())
		m.apps.apps = []connectedApp{{ID: "a", Name: "cursor", Kind: "bot", ClientName: "Cursor", RedirectURIs: []string{"https://www.cursor.com/cb"}, CreatedAt: stamp}, {ID: "b", Name: "iphone", Kind: "owner_app", ClientName: "iPhone Safari", RedirectURIs: []string{"https://phone.example/cb"}, CreatedAt: stamp, LastUsedAt: &used}}
		golden(t, name("list"), m.View())
		m.apps.stage = "confirm"
		golden(t, name("revoke"), m.View())
		m.apps.stage = "code"
		m.apps.input = textinput.New()
		m.apps.input.Focus()
		golden(t, name("code"), m.View())
		m.apps.stage = "approve"
		m.apps.pending = pendingApp{ID: "p", ClientName: "Cursor", RedirectHost: "www.cursor.com"}
		m.apps.kind = "bot"
		m.apps.input.SetValue("cursor")
		m.apps.input.CursorEnd()
		m.apps.input.Focus()
		m.apps.editingName = true
		golden(t, name("approval"), m.View())
	}
}

func TestConnectedAppRenameSettingsGoldens(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(sz[0], sz[1])
		m.tab = settingsTab
		stamp := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		m.apps.apps = []connectedApp{{ID: "a", Name: "grok-bot", Kind: "bot", ClientName: "Grok Bot", CreatedAt: stamp}}
		m.apps.stage = "rename"
		m.apps.index = 0
		m.apps.input = textinput.New()
		m.apps.input.Prompt = "name for grok-bot> "
		m.apps.input.SetValue("grok-bot")
		m.apps.input.CursorEnd()
		m.apps.input.Focus()
		golden(t, fmt.Sprintf("connected_apps_rename_%d", sz[0]), m.View())
	}
}

func TestConnectedAppsHelp(t *testing.T) {
	m := newModel(120, 34)
	m.mode = modeApps
	help := strings.Join(m.helpContent(120), "\n")
	for _, want := range []string{"Connected apps", "add", "revoke", "approve", "refuse", "change kind"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestConnectedAppsPolishedViews(t *testing.T) {
	m := newModel(80, 30)
	m.mode = modeApps
	m.apps.apps = []connectedApp{{Name: "cursor", Kind: "bot", ClientName: "Cursor", RedirectURIs: []string{"https://www.cursor.com/cb"}, CreatedAt: m.now()}}
	view := ansi.Strip(strings.Join(m.appsView(80, 20), "\n"))
	for _, s := range []string{"NAME", "KIND", "CLIENT", "RETURN HOST", "CONNECTED", "LAST USED", "▸ cursor", "today 14:05", "never"} {
		if !strings.Contains(view, s) {
			t.Errorf("list missing %q: %s", s, view)
		}
	}
	m.apps.stage = "approve"
	m.apps.kind = "bot"
	m.apps.pending = pendingApp{ClientName: "Cursor", RedirectHost: "www.cursor.com"}
	m.apps.input = textinput.New()
	m.apps.input.SetValue("cursor")
	view = ansi.Strip(strings.Join(m.appsView(80, 20), "\n"))
	for _, s := range []string{"Approve this app?", "Client       Cursor", "Return host  www.cursor.com", "Kind         ‹ bot ›", "Name         cursor", "Approve only if you just pressed connect"} {
		if !strings.Contains(view, s) {
			t.Errorf("approval missing %q: %s", s, view)
		}
	}
	m.apps.stage = "code"
	m, _ = m.onAppsResult(appsResult{op: "apps.pending", err: errors.New("too many tries: 3 minutes")})
	if m.apps.err != "Too many wrong codes. Try again in 3 minutes." {
		t.Errorf("countdown %q", m.apps.err)
	}
}

func TestConnectedAppsColumnsFit(t *testing.T) {
	m := newModel(80, 30)
	m.mode = modeApps
	m.apps.apps = []connectedApp{{Name: strings.Repeat("name", 20), Kind: "bot", ClientName: strings.Repeat("client", 15), RedirectURIs: []string{"https://" + strings.Repeat("host", 15) + ".example/cb"}, CreatedAt: m.now()}}
	lines := m.appsView(80, 20)
	for _, line := range lines {
		if ansi.StringWidth(line) > 80 {
			t.Fatalf("line exceeds width: %q", line)
		}
	}
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "…") {
		t.Fatal("long cells were not truncated")
	}
}

func TestConnectedAppsPhoneDefault(t *testing.T) {
	m := newModel(80, 30)
	m.mode = modeApps
	m, _ = m.onAppsResult(appsResult{op: "apps.pending", raw: json.RawMessage(`{"pending":{"ID":"p","ClientName":"iPhone Safari","DefaultKind":"owner_app"}}`)})
	if m.apps.kind != "owner_app" || m.apps.input.Value() != "iPhone Safari" {
		t.Fatalf("phone default: kind=%q name=%q", m.apps.kind, m.apps.input.Value())
	}
}

func TestApprovalNameFocusedAndSettingsRename(t *testing.T) {
	f := &connectedAppsFake{result: `{}`}
	m := newModel(120, 34)
	m.src = f
	m.mode = modeApps
	m, _ = m.onAppsResult(appsResult{op: "apps.pending", raw: json.RawMessage(`{"pending":{"ID":"p","ClientName":"Grok Bot"}}`)})
	if !m.apps.editingName {
		t.Fatal("name is not focused")
	}
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if m.apps.input.Value() != "grok-bot2" {
		t.Fatalf("typing: %q", m.apps.input.Value())
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("approval did not issue a call")
	}
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.approve" || f.params["name"] != "grok-bot2" {
		t.Fatalf("approval: %s %+v", f.op, f.params)
	}
	m.mode = modeNormal
	m.apps.loading = false
	m.apps.stage = ""
	m.apps.apps = []connectedApp{{ID: "app_1", Name: "grok-bot", Kind: "bot"}}
	m.tab = settingsTab
	items := m.settingsItems()
	for i, item := range items {
		if item.kind == "app" {
			m.settingsRow = i
			break
		}
	}
	var next tea.Model
	next, cmd = m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.apps.stage != "rename" {
		t.Fatalf("enter did not open rename field: stage=%q cmd=%v row=%d items=%+v", m.apps.stage, cmd != nil, m.settingsRow, m.settingsItems())
	}
	m.apps.input.SetValue(" ")
	next, cmd = m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.apps.stage != "rename" || m.apps.err == "" {
		t.Fatal("empty app name was not refused")
	}
	m.apps.input.SetValue("new bot")
	next, cmd = m.settingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("rename did not issue a call")
	}
	updated, _ := m.Update(cmd())
	if f.op != "apps.rename" || f.params["name"] != "new bot" {
		t.Fatalf("rename call: %s %+v", f.op, f.params)
	}
	m, _ = m.onAppsResult(appsResult{op: "apps.list", raw: json.RawMessage(`{"apps":[{"ID":"app_1","Name":"new bot","Kind":"bot"}]}`)})
	if len(m.apps.apps) != 1 || m.apps.apps[0].Name != "new bot" {
		t.Fatalf("renamed app not shown in list: %+v", m.apps.apps)
	}
	_ = updated
}

func TestAppsPublicKeys(t *testing.T) {
	m := newModel(120, 34)
	m.mode = modeApps
	m = keys(m, "a")
	if m.apps.stage != "public-confirm" {
		t.Fatalf("add while off: %s", m.apps.stage)
	}
	m = keys(m, "n")
	if m.apps.stage != "" {
		t.Fatal(m.apps.stage)
	}
	_, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil {
		t.Fatal("p does nothing")
	}
	m = keys(m, "u")
	if m.apps.stage != "url" {
		t.Fatal("u does not ask for URL")
	}
}

func TestAppsPublicSwitchAndURLFlow(t *testing.T) {
	f := &connectedAppsFake{result: `{"on":true,"url":"https://test.example","port":8443}`}
	m := newModel(120, 34)
	m.mode = modeApps
	m.src = f
	var cmd tea.Cmd
	m = keys(m, "a")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.public.on" || m.apps.stage != "code" || !m.apps.public.On {
		t.Fatalf("add on: %+v", m.apps)
	}
	m = keys(m, "esc")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	f.result = `{"on":false}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.public.off" || m.apps.public.On {
		t.Fatal("off failed")
	}
	m = keys(m, "u")
	m.apps.input.SetValue("https://tunnel.example")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	f.result = `{"on":true,"url":"https://tunnel.example"}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.public.url" || f.params["url"] != "https://tunnel.example" || !m.apps.public.On {
		t.Fatal("URL failed")
	}
}
