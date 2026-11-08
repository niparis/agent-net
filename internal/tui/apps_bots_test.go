package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

func botScreenModel(w int) Model {
	m := newModel(w, 34)
	m.activeOnly = false
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "s-bot", Harness: "bot", Profile: protocol.Profile{Name: "weather-bot"}, NodeID: "app-test", Online: true, LastSeen: m.now().Add(-time.Minute).Unix()})
	return m
}
func TestSessionsBotsGroupAndKeys(t *testing.T) {
	m := botScreenModel(120)
	if len(m.machineRows()) != len(m.snap.Nodes) {
		t.Fatal("bot creates a phantom machine")
	}
	groups := m.groups()
	if groups[len(groups)-1].host != "Bots" {
		t.Fatalf("last group: %+v", groups[len(groups)-1])
	}
	m.selected = "s-bot"
	m.ensureSelection()
	next, _ := m.startEnter()
	if next.tab != messagesTab || next.filter != "s-bot" {
		t.Fatal("bot enter did not filter Messages")
	}
	next, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	if !strings.Contains(strings.Join(next.appsView(120, 24), "\n"), "Revoke weather-bot?") {
		t.Fatal("bot revoke does not confirm")
	}
}
func TestAppsPublicAndBotsScreens(t *testing.T) {
	for _, w := range []int{80, 120} {
		m := quietFixtures(botScreenModel(w))
		golden(t, fmt.Sprintf("sessions_bots_%d", w), m.testView())
		if w == 120 && !strings.Contains(m.testView(), "last call") {
			t.Fatal("bot last call missing")
		}
		m.mode = modeApps
		m.apps.apps = []connectedApp{{ID: "app-test", Name: "weather-bot", Kind: "bot", ClientName: "Cursor", RedirectURIs: []string{"https://www.cursor.com/cb"}, CreatedAt: m.now()}, {ID: "phone", Name: "phone", Kind: "owner_app", ClientName: "Safari", CreatedAt: m.now()}}
		golden(t, fmt.Sprintf("connected_apps_public_off_%d", w), m.testView())
		m.apps.public.On = true
		m.apps.public.URL = "https://host.example.ts.net:8443"
		golden(t, fmt.Sprintf("connected_apps_public_on_%d", w), m.testView())
		m.apps.public.On = false
		m.apps.public.Error = "On research-gateway (the gateway machine, where internet access runs), run once in a terminal: sudo tailscale set --operator=$USER. Then press p again."
		golden(t, fmt.Sprintf("connected_apps_public_error_%d", w), m.testView())
	}
}

func TestBotColumnsAndPublicAddresses(t *testing.T) {
	m := botScreenModel(120)
	view := m.testView()
	if !strings.Contains(view, "weather-bot") || !strings.Contains(view, "last call 14:04") {
		t.Fatalf("bot columns:\n%s", view)
	}
	m.apps.public.On = true
	m.apps.public.URL = "https://host.example.ts.net:8443"
	line := strings.Join(m.appsView(120, 24), "\n")
	if !strings.Contains(line, "phone https://host.example.ts.net:8443/   bots https://host.example.ts.net:8443/mcp") {
		t.Fatal(line)
	}
}
