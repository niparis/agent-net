package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func TestGraphCardReturnKeepsSelection(t *testing.T) {
	m := graphModel(120, 34)
	m.tab = 3
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	camera := m.graphState.Camera
	m = keys(m, "enter")
	if !m.mapOn || m.tab != 3 || m.mp.selected != "project-02" {
		t.Fatalf("Enter did not open card: tab=%d card=%v node=%s", m.tab, m.mapOn, m.mp.selected)
	}
	m = keys(m, "esc")
	if m.mapOn || m.tab != 3 || m.graphState.Graph.Selected() != "project-02" || m.graphState.Camera != camera {
		t.Fatal("Esc did not return to selected graph node")
	}
}
func TestNetworkTabsAndAliases(t *testing.T) {
	m := mapModel(120, 34, fakeGraph())
	if len(tabNames) != 5 || !strings.Contains(ansi.Strip(m.titleLine(120)), "4 Graph  5 Tools") {
		t.Fatalf("wrong tabs: %v", tabNames)
	}
	for _, key := range []string{"4", "M", "G"} {
		n := keys(m, key)
		if n.tab != 3 || n.mapOn {
			t.Fatalf("%s did not open Graph", key)
		}
	}
}
func TestGraphFindSelectsMatch(t *testing.T) {
	m := graphModel(120, 34)
	m.tab = 3
	m = keys(m, "/", "project-12", "enter")
	if m.graphState.Graph.Selected() != "project-12" || m.mapOn || m.mp.finding {
		t.Fatal("graph search did not select match")
	}
}
func TestGraphCardLearnedUndo(t *testing.T) {
	m := graphModel(120, 40)
	m.tab = 3
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	m.snap.Learned = []protocol.LearnedSuggestion{{ID: 1, Node: "project-02", Field: "needs", Phrase: "ops", Status: "applied", By: "learned", HistoryID: 42}, {ID: 2, Node: "project-02", Field: "needs", Phrase: "backup", Status: "applied", By: "learned", HistoryID: 43}}
	m = keys(m, "enter", "U")
	src := &learnedSource{}
	m.src = src
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if cmd == nil {
		t.Fatal("card undo missing")
	}
	cmd()
	raw, _ := json.Marshal(src.params)
	if !strings.Contains(string(raw), "43") {
		t.Fatalf("undo did not use the change selected with U: %s", raw)
	}
	if src.method != "graph_undo" {
		t.Fatalf("undo called %s", src.method)
	}
}
func TestOnboardingNetworkUsesGraph(t *testing.T) {
	m, _ := onboardingModel()
	for _, step := range []string{"see", "see-machine", "talk", "cto"} {
		m.onboarding.Steps[step] = "done"
	}
	if step := m.onboardingStep(); step.id != "map" || !strings.Contains(step.label, "Graph") {
		t.Fatalf("network step: %+v", step)
	}
}

func TestGraphCardHasNoSeparateNavigationAndOpensSessions(t *testing.T) {
	m := graphModel(120, 34)
	m = keys(m, "enter")
	selected := m.mp.selected
	camera := m.graphState.Camera
	m = keys(m, "left", "right", "up", "down", "z")
	if m.mp.selected != selected || m.graphState.Camera != camera {
		t.Fatal("card changed network selection or camera")
	}
	m = keys(m, "enter")
	if m.tab != 0 || m.mapOn || m.filter != selected {
		t.Fatal("card sessions did not open filtered Sessions")
	}
}

func TestGraphSearchCancelAndDisplayName(t *testing.T) {
	m := graphModel(80, 24)
	selected := m.graphState.Graph.Selected()
	m = keys(m, "/", "nothing", "esc")
	if m.graphState.Graph.Selected() != selected {
		t.Fatal("cancel changed selection")
	}
	m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "project-12", DisplayName: "Evidence API"})
	m = keys(m, "/", "Evidence", "enter")
	if m.graphState.Graph.Selected() != "project-12" {
		t.Fatal("display name did not match")
	}
}
