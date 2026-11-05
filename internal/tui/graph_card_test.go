package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func TestGraphCardReturnKeepsSelection(t *testing.T) {
	m := graphModel(120, 34)
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	camera := m.graphState.Camera
	m = keys(m, "enter")
	if !m.mapOn || m.tab != graphTab || m.mp.selected != "project-02" {
		t.Fatalf("Enter did not open card: tab=%d card=%v node=%s", m.tab, m.mapOn, m.mp.selected)
	}
	m = keys(m, "esc")
	if m.mapOn || m.tab != graphTab || m.graphState.Graph.Selected() != "project-02" || m.graphState.Camera != camera {
		t.Fatal("Esc did not return to selected graph node")
	}
}
func TestNetworkTabUsesDigitOnly(t *testing.T) {
	m := mapModel(120, 34, fakeGraph())
	if len(tabNames) != 8 || !strings.Contains(ansi.Strip(m.titleLine(120)), "2 Graph 3 Quotas") || !strings.Contains(ansi.Strip(m.titleLine(120)), "7 Inbox") {
		t.Fatalf("wrong tabs: %v", tabNames)
	}
	for _, key := range []string{"2"} {
		n := keys(m, key)
		if n.tab != graphTab || n.mapOn {
			t.Fatalf("%s did not open Graph", key)
		}
	}
	for _, key := range []string{"M", "G"} {
		if n := keys(m, key); n.tab == graphTab {
			t.Fatalf("%s still opens Graph", key)
		}
	}
}
func TestGraphFindSelectsMatch(t *testing.T) {
	m := graphModel(120, 34)
	m.tab = graphTab
	m = keys(m, "/", "project-12", "enter")
	if m.graphState.Graph.Selected() != "project-12" || m.mapOn || m.mp.finding {
		t.Fatal("graph search did not select match")
	}
}
func TestGraphCardLearnedUndo(t *testing.T) {
	m := graphModel(120, 40)
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	m.snap.Learned = []protocol.LearnedSuggestion{{ID: 1, Node: "project-02", Field: "needs", Phrase: "ops", Status: "applied", By: "learned", HistoryID: 42}, {ID: 2, Node: "project-02", Field: "needs", Phrase: "backup", Status: "applied", By: "learned", HistoryID: 43}}
	m = keys(m, "enter", ".")
	src := &learnedSource{}
	m.src = src
	_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: []rune("u")[0]})
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
	m = keys(m, "/", "nothing", "esc", "esc")
	if m.graphState.Graph.Selected() != selected {
		t.Fatal("cancel changed selection")
	}
	m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "project-12", DisplayName: "Evidence API"})
	m = keys(m, "/", "Evidence", "enter")
	if m.graphState.Graph.Selected() != "project-12" {
		t.Fatal("display name did not match")
	}
}

func TestGraphDeliveryListsAllConfiguredSteps(t *testing.T) {
	m := graphModel(80, 24)
	lines := strings.Join(m.graphDeliveryOwners("project-02"), " ")
	for _, step := range []string{"accept", "test", "review", "merge", "release", "verify"} {
		if !strings.Contains(lines, step) {
			t.Errorf("missing %s in %q", step, lines)
		}
	}
}

func TestGraphOpensDeliveryForProject(t *testing.T) {
	m := graphModel(80, 24)
	m.tab = graphTab
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	m = keys(m, "d")
	if m.tab != deliveryTab || m.delivery.project != "project-02" {
		t.Fatalf("tab %d project %q", m.tab, m.delivery.project)
	}
	m = keys(m, "esc")
	if m.delivery.project != "" {
		t.Fatal("escape did not clear project")
	}
}

func TestGraphFooterHeightAndCaller(t *testing.T) {
	m := graphModel(80, 24)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	_, h1 := m.graphSize()
	owners := strings.Join(m.graphDeliveryOwners("project-02"), " ")
	if !strings.Contains(owners, ": caller") {
		t.Fatalf("caller missing: %s", owners)
	}
	m.mp.edges = append(m.mp.edges, protocol.Edge{From: "/delivery/merger", To: "project-03", Kind: protocol.DeliveryStepEdge(protocol.StepTest)})
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-03"})
	_, h2 := m.graphSize()
	if h1 != h2 {
		t.Fatalf("canvas heights changed: %d and %d", h1, h2)
	}
	if lines := m.graphOwnerLines(80); len(lines) != 2 {
		t.Fatalf("owner lines: %v", lines)
	}
}

func TestGraphOwnerLinesPackOverflow(t *testing.T) {
	m := graphModel(80, 24)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-03"})
	m.mp.edges = append(m.mp.edges,
		protocol.Edge{From: "/delivery/merger", To: "project-03", Kind: protocol.DeliveryStepEdge(protocol.StepTest)},
		protocol.Edge{From: "project-02", To: "project-03", Kind: protocol.DeliveryStepEdge(protocol.StepReview)})
	owners := m.graphDeliveryOwners("project-03")
	if len(owners) < 3 {
		t.Fatalf("need three owners: %v", owners)
	}
	lines := m.graphOwnerLines(80)
	if len(lines) != 2 || !strings.Contains(lines[1], "; ") {
		t.Fatalf("overflow not packed: %v", lines)
	}
	if ansi.StringWidth(lines[1]) > 78 {
		t.Fatalf("overflow too wide: %s", lines[1])
	}
}

func TestGraphDeliveryFooterGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, project := range []string{"project-02", "project-03"} {
			m := graphModel(size[0], size[1])
			m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: project})
			m.mp.edges = append(m.mp.edges, protocol.Edge{From: "/delivery/merger", To: "project-03", Kind: protocol.DeliveryStepEdge(protocol.StepTest)})
			golden(t, fmt.Sprintf("graph_footer_%s_%dx%d", project, size[0], size[1]), m.testView())
		}
	}
}
