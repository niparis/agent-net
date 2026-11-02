package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"
)

func TestConnectedAppsSettingsEntry(t *testing.T) {
	m := keys(newModel(120, 34), "S")
	if !strings.Contains(strings.Join(m.settingsMenuView(120), "\n"), "Connected apps") {
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
	m = keys(m, "a")
	m.apps.input.SetValue("ABC-234")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	f.result = `{"pending":{"ID":"p1","ClientName":"Cursor","RedirectHost":"client.example"}}`
	m = runAppsCmd(t, m, cmd)
	if f.op != "apps.pending" || f.params["code"] != "ABC-234" || m.apps.input.Value() != "cursor" {
		t.Fatalf("lookup: %+v %+v", f, m.apps)
	}
	m = keys(m, "right", "tab")
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
		m.apps.apps = []connectedApp{{ID: "a", Name: "cursor", Kind: "bot", ClientName: "Cursor", RedirectURIs: []string{"https://client.example/cb"}, CreatedAt: stamp}, {ID: "b", Name: "phone", Kind: "owner_app", ClientName: "Phone", RedirectURIs: []string{"https://phone.example/cb"}, CreatedAt: stamp, LastUsedAt: &used}}
		golden(t, name("list"), m.View())
		m.apps.stage = "confirm"
		golden(t, name("revoke"), m.View())
		m.apps.stage = "code"
		m.apps.input = textinput.New()
		m.apps.input.Focus()
		golden(t, name("code"), m.View())
		m.apps.stage = "approve"
		m.apps.pending = pendingApp{ID: "p", ClientName: "Cursor", RedirectHost: "client.example"}
		m.apps.kind = "bot"
		m.apps.input.SetValue("cursor")
		golden(t, name("approval"), m.View())
	}
}

func TestConnectedAppsHelp(t *testing.T) {
	m := newModel(120, 34)
	help := strings.Join(m.helpContent(120), "\n")
	for _, want := range []string{"Connected apps", "add", "revoke", "approve", "refuse", "change kind", "edit name"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
