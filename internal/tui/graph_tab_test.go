package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	m.graphState, _ = graphview.Update(m.graphState, graphview.TalkLoaded{Counts: []protocol.GraphTalkCount{{From: "project-01", To: "project-02", Count: 7, HasEdge: true, Voice: true}, {From: "project-07", To: "project-11", Count: 23}}})
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
	m = keys(m, "t")
	if !m.graphState.HideTalk {
		t.Fatal("talk toggle failed")
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
		plain := ansi.Strip(got)
		if strings.Contains(plain, "…") {
			t.Fatalf("truncated graph label: %s", plain)
		}
		for _, name := range regexp.MustCompile(`project-[[:alnum:]-]*`).FindAllString(plain, -1) {
			if !regexp.MustCompile(`^project-[0-9]{2}$`).MatchString(name) {
				t.Errorf("partial project label %q", name)
			}
		}
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
	out := (graphview.Braille{}).Render(graphview.BuildScene(graphview.State{Graph: m.graphState.Graph, Camera: m.graphState.Camera, Now: t0}, graphview.Viewport{Width: float64(w), Height: float64(h)}), graphview.Size{Width: w, Height: h})
	for _, node := range m.graphState.Graph.Nodes {
		found := false
		for y, line := range out.Lines {
			i := strings.Index(line, node.Name)
			if i < 0 {
				continue
			}
			start := len([]rune(line[:i]))
			end := start + len([]rune(node.Name))
			for _, x := range []int{start - 2, end + 1} {
				if x >= 0 && x < w && out.Hits[graphview.Cell{X: x, Y: y}] == node.Name {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("label %q is not one space from its disc", node.Name)
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

func TestGraphTalkSnapshotReplaysOnlyNewerFeed(t *testing.T) {
	m := graphModel(80, 24)
	m.graphTalkLoading = true
	m.graphState, _ = graphview.Update(m.graphState, graphview.TalkLoaded{Counts: []protocol.GraphTalkCount{{From: "project-01", To: "project-02", Count: 1}}})
	m.graphTalkPending = []graphview.FeedEvents{{Now: t0, Events: []protocol.FeedEvent{
		{Event: protocol.Event{ID: 4, Kind: "msg.stored", Time: t0}, From: "session-1", To: "session-2"},
		{Event: protocol.Event{ID: 6, Kind: "msg.stored", Time: t0}, From: "session-1", To: "session-2"},
	}}}
	next, _ := m.Update(graphTalkMsg{at: t0, result: protocol.GraphTalkResult{LastID: 5, Counts: []protocol.GraphTalkCount{{From: "project-01", To: "project-02", Count: 4}}}})
	got := next.(Model).graphState.Talk
	if len(got) != 1 || got[0].Count != 5 {
		t.Fatalf("snapshot replay: %+v", got)
	}
}

func TestGraphHintsFitWithoutCuttingWords(t *testing.T) {
	for _, paused := range []bool{false, true} {
		m := graphModel(80, 24)
		m.graphState.Paused = paused
		lines := m.graphView(80, 16)
		line := ansi.Strip(lines[len(lines)-1])
		if !strings.HasSuffix(line, "? keys") || !strings.Contains(line, "t talk") || strings.Contains(line, "…") || ansi.StringWidth(line) > 80 {
			t.Fatalf("paused=%v: invalid hint line %q", paused, line)
		}
		if strings.Contains(line, "space pause") || strings.Contains(line, "enter Map") || !strings.Contains(line, "arrows select") {
			t.Fatalf("paused=%v: incorrect hint priority %q", paused, line)
		}
	}
}

func TestGraphHintsDropInPriorityOrder(t *testing.T) {
	for _, tc := range []struct {
		width           int
		present, absent []string
	}{
		{80, []string{"arrows select", "t talk", "? keys"}, []string{"space pause", "enter Map", "0 fit"}},
		{70, []string{"arrows select", "t talk", "? keys"}, []string{"space pause", "enter Map", "0 fit"}},
		{60, []string{"t talk", "? keys"}, []string{"space pause", "enter Map", "0 fit"}},
		{40, []string{"t talk", "? keys"}, []string{"space pause", "enter Map", "0 fit"}},
	} {
		line := graphHintLine(tc.width, false)
		if ansi.StringWidth(line) > tc.width || strings.Contains(line, "…") || !strings.HasSuffix(line, "? keys") {
			t.Fatalf("width %d: %q", tc.width, line)
		}
		for _, h := range tc.present {
			if !strings.Contains(line, h) {
				t.Errorf("width %d missing %q: %q", tc.width, h, line)
			}
		}
		for _, h := range tc.absent {
			if strings.Contains(line, h) {
				t.Errorf("width %d has %q: %q", tc.width, h, line)
			}
		}
	}
}

func TestGraphHelpDescribesNavigation(t *testing.T) {
	m := graphModel(120, 34)
	m.graphHelp = true
	text := strings.Join(m.graphView(120, 20), " ")
	for _, want := range []string{"arrows select", "shift+arrows", "[ ] neighbours"} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q: %s", want, text)
		}
	}
}

func TestGraphMouseSelectsLabelAndIgnoresEmpty(t *testing.T) {
	m := graphModel(120, 34)
	w, h := m.graphSize()
	scene := graphview.BuildScene(m.graphState, graphview.Viewport{Width: float64(w), Height: float64(h)})
	for i := range scene.Nodes {
		scene.Nodes[i].Label = m.projectName(scene.Nodes[i].Name)
	}
	out := (graphview.Braille{}).Render(scene, graphview.Size{Width: w, Height: h})
	var cell graphview.Cell
	target := ""
	for p, id := range out.Hits {
		if id != m.graphState.Graph.Selected() && strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789", []rune(out.Lines[p.Y])[p.X]) {
			cell, target = p, id
			break
		}
	}
	if target == "" {
		t.Fatal("no label cell")
	}
	next, _ := m.Update(tea.MouseMsg{X: cell.X + 2, Y: cell.Y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.graphState.Graph.Selected() != target {
		t.Fatalf("selected %q, want %q", m.graphState.Graph.Selected(), target)
	}
	next, _ = m.Update(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if next.(Model).graphState.Graph.Selected() != target {
		t.Fatal("empty click changed selection")
	}
}

func TestGraphMouseDisabledWhenLeavingOrFocusingCTO(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'1'}}, {Type: tea.KeyTab}} {
		m := graphModel(120, 34)
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatal("missing mouse command")
		}
		msg := cmd()
		batch, _ := msg.(tea.BatchMsg)
		found := fmt.Sprintf("%T", msg) == fmt.Sprintf("%T", tea.DisableMouse())
		for _, c := range batch {
			if fmt.Sprintf("%T", c()) == fmt.Sprintf("%T", tea.DisableMouse()) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s did not disable mouse", key.String())
		}
	}
}

func TestGraphQuitDisablesMouseBeforeQuit(t *testing.T) {
	m := graphModel(120, 34)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("no quit command")
	}
	// A sequence preserves ordering between disabling mouse and stopping Bubble Tea.
	if fmt.Sprintf("%T", cmd()) != fmt.Sprintf("%T", tea.Sequence(tea.DisableMouse, tea.Quit)()) {
		t.Fatal("quit must disable mouse first")
	}
}

func TestGraphVoiceLegend(t *testing.T) {
	if legend := graphLegend(52); !strings.Contains(legend, "gold: your words") || strings.Contains(legend, "pink:") || ansi.StringWidth(legend) > 52 {
		t.Fatalf("narrow legend: %q", legend)
	}
	for _, w := range []int{80, 120} {
		lines := graphModel(w, 34).graphView(w, 20)
		found := false
		for _, line := range lines {
			line = ansi.Strip(line)
			if strings.Contains(line, "gold: your words") {
				found = true
				if ansi.StringWidth(line) > w || strings.Contains(line, "…") {
					t.Fatalf("legend: %q", line)
				}
			}
		}
		if !found {
			t.Fatalf("voice legend missing at %d", w)
		}
	}
}
