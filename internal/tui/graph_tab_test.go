package tui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func graphFixture() protocol.GraphView {
	var v protocol.GraphView
	for i := 0; i < 15; i++ {
		n := protocol.Node{Name: fmt.Sprintf("project-%02d", i), Kind: "component"}
		if i == 0 {
			n.Name = "architect"
			n.Kind = "architect"
		}
		if i < 6 {
			n.SessionCount = 1
			n.Sessions = []protocol.GraphSession{{ID: fmt.Sprintf("session-%d", i), Status: "idle"}}
		}
		if i == 1 {
			n.Sessions[0].Status = "busy"
		}
		v.Nodes = append(v.Nodes, n)
	}
	v.Edges = []protocol.Edge{{From: "architect", To: "project-01", Kind: "governs"}, {From: "project-01", To: "project-02", Kind: "depends"}, {From: "project-02", To: "project-03", Kind: "deploys"}, {From: "*", To: "project-03", Kind: "depends"}, {From: "project-03", To: "project-04", Kind: "depends"}, {From: "project-04", To: "project-05", Kind: "depends"}}
	return v
}
func graphModel(w, h int) Model {
	m := mapModel(w, h, graphFixture())
	m.applyMap(mapMsg{view: graphFixture(), at: t0})
	m.tab = 5
	gw, gh := m.graphSize()
	m.graphState.Camera = m.graphState.Graph.Fit(gw, gh)
	m.graphState.Graph.Message("project-01", "project-02", "request", t0.Add(-time.Second))
	m.graphState.Graph.Message("project-04", "project-05", "reply", t0.Add(-2*time.Second))
	return m
}
func TestGraphTabKeys(t *testing.T) {
	m := graphModel(120, 34)
	start := m.graphState.Graph.Selected()
	m = keys(m, "]")
	if m.graphState.Graph.Selected() == start {
		t.Fatal("selection did not move")
	}
	chosen := m.graphState.Graph.Selected()
	old := m.graphState.Camera
	m = keys(m, "+", "h")
	if m.graphState.Camera.Zoom <= old.Zoom || m.graphState.Camera.Center.X >= old.Center.X {
		t.Fatal("zoom or pan failed")
	}
	m = keys(m, "0")
	if m.graphState.Camera.Zoom != old.Zoom {
		t.Fatal("fit failed")
	}
	m = keys(m, "o")
	if !m.graphState.Orbit {
		t.Fatal("orbit failed")
	}
	m = keys(m, " ")
	if !m.graphState.Paused {
		t.Fatal("pause failed")
	}
	m = keys(m, "enter")
	if !m.mapOn || m.mp.selected != chosen {
		t.Fatal("enter did not open selected map node")
	}
	n := mapModel(80, 24, graphFixture())
	next, cmd := n.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6")})
	n = next.(Model)
	if n.tab != 5 || cmd == nil {
		t.Fatal("6 did not open graph")
	}
}
func TestGraphGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := graphModel(size[0], size[1])
		got := m.View()
		if strings.Count(got, "•") < 2 {
			t.Fatalf("two comets must be visible at %dx%d:\n%s", size[0], size[1], got)
		}
		if !strings.Contains(got, "Graph") {
			t.Fatal("graph absent")
		}
		golden(t, fmt.Sprintf("graph_%dx%d", size[0], size[1]), got)
	}
}

func TestGraphAnimationTick(t *testing.T) {
	m := graphModel(80, 24)
	_, cmd := m.Update(graphTickMsg{})
	if cmd == nil {
		t.Fatal("graph did not schedule animation")
	}
	m.tab = 0
	_, cmd = m.Update(graphTickMsg{})
	if cmd != nil {
		t.Fatal("animation continued on another tab")
	}
}

func TestGraphConnectedNodesSpanRows(t *testing.T) {
	m := graphModel(120, 34)
	w, h := m.graphSize()
	low, high := h, 0
	for _, n := range []string{"architect", "project-01", "project-02", "project-03", "project-04", "project-05"} {
		_, y := m.graphState.Camera.Project(m.graphState.Graph.Positions[n], w, h)
		row := int(y)
		low = min(low, row)
		high = max(high, row)
	}
	if high-low < 4 {
		t.Fatalf("connected nodes span only %d rows (%d to %d)", high-low+1, low, high)
	}
}

func TestGraphFixtureLabelsStayWithTheirMarkers(t *testing.T) {
	m := graphModel(120, 34)
	w, h := m.graphSize()
	lines := (graphview.Braille{}).Render(graphview.BuildScene(graphview.State{Graph: m.graphState.Graph, Camera: m.graphState.Camera, Now: t0}, graphview.Viewport{Width: float64(w), Height: float64(h)}), graphview.Size{Width: w, Height: h}).Lines
	for _, node := range m.graphState.Graph.Nodes {
		x, y := m.graphState.Camera.Project(m.graphState.Graph.Positions[node.Name], w, h)
		cx := int(math.Round(x*2)) / 2
		cy := int(math.Round(y*4)) / 4
		found := false
		for row, line := range lines {
			i := strings.Index(line, node.Name)
			if i < 0 {
				continue
			}
			start := len([]rune(line[:i]))
			end := start + len([]rune(node.Name))
			if row == cy && (start == cx+2 || end == cx-1) || absGraph(row-cy) == 1 && start <= cx && end > cx {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("label %q is not adjacent to marker at %d,%d", node.Name, cx, cy)
		}
	}
}
func absGraph(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestGraphEnterOpensEmptyMap(t *testing.T) {
	m := mapModel(80, 24, protocol.GraphView{})
	m.applyMap(mapMsg{view: protocol.GraphView{}, at: t0})
	m.tab = 5
	m = keys(m, "enter")
	if !m.mapOn {
		t.Fatal("enter should open the map even without a selected node")
	}
}
