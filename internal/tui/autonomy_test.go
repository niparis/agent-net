package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMapCardShowsAutonomyAndTogglesIt(t *testing.T) {
	g := richGraph()
	for i := range g.Nodes {
		if g.Nodes[i].Name == "tenebrae" {
			g.Nodes[i].Settings = map[string]string{protocol.SettingAutonomy: protocol.AutonomyWake}
		}
	}
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "tenebrae"
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "autonomy") || !strings.Contains(view, "wake") {
		t.Fatalf("the card does not show autonomy:\n%s", view)
	}
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("a did not start an autonomy change")
	}
	m = send(m, cmd())
	src := m.src.(*fakeSource)
	if len(src.autonomyCalls) != 1 || src.autonomyCalls[0].node != "tenebrae" || src.autonomyCalls[0].value != protocol.AutonomyOff {
		t.Fatalf("autonomy calls = %+v", src.autonomyCalls)
	}
	if !strings.Contains(m.notice.text, "Autonomy off") {
		t.Fatalf("notice = %q", m.notice.text)
	}
}
