package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMapCardShowsWorkModeBesideTheRules(t *testing.T) {
	g := richGraph()
	for i := range g.Nodes {
		if g.Nodes[i].Name == "tenebrae" {
			g.Nodes[i].Charter.WorkMode = protocol.WorkModeWorkersCode
			g.Nodes[i].Charter.Sources = &protocol.CharterSources{"work_mode": {{Level: "team", Scope: "forge", Value: protocol.WorkModeWorkersCode}}}
		}
	}
	m := openMap(t, mapModel(120, 40, g))
	m.mp.selected = "tenebrae"
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "work_mode") || !strings.Contains(view, "workers_code") || !strings.Contains(view, "team forge") {
		t.Fatalf("the card does not show the work mode:\n%s", view)
	}
}

func TestMapCardWorkModeDefaultsToOnRequest(t *testing.T) {
	m := openMap(t, mapModel(120, 40, richGraph()))
	m.mp.selected = "tenebrae"
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "work_mode") || !strings.Contains(view, "on_request") {
		t.Fatalf("the card does not show the default work mode:\n%s", view)
	}
}
