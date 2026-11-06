package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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
	m.graphPixel.profile = colorprofile.TrueColor
	m.applyMap(mapMsg{view: graphFixture(), at: t0})
	m.tab = graphTab
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
	m = keys(m, ".")
	if m.graphState.Graph.Selected() == start {
		t.Fatal("selection did not move")
	}
	chosen := m.graphState.Graph.Selected()
	old := m.graphState.Camera
	m = keys(m, "+", "shift+left")
	if m.graphState.Camera.Zoom <= old.Zoom || m.graphState.Camera.Center.X >= old.Center.X {
		t.Fatal("zoom or pan failed")
	}
	// Fit happens when the graph loads; 0 is no longer a key.
	zoomed := m.graphState.Camera.Zoom
	tools := m.tl.search
	m = keys(m, "0", "t", "o", " ")
	if m.graphState.Camera.Zoom != zoomed || m.tab != graphTab || m.tl.search != tools || m.graphState.Orbit || m.graphState.Paused {
		t.Fatal("a removed graph key still changed the graph")
	}
	m = keys(m, "enter")
	if !m.mapOn || m.mp.selected != chosen {
		t.Fatal("enter did not open selected map node")
	}
	n := mapModel(80, 24, graphFixture())
	next, cmd := n.Update(tea.KeyPressMsg{Text: "2", Code: []rune("2")[0]})
	n = next.(Model)
	if n.tab != graphTab || cmd == nil {
		t.Fatal("2 did not open graph")
	}
}
func TestGraphGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := graphModel(size[0], size[1])
		// A central node shows the accented neighbourhood and the dimmed rest.
		m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-03"})
		got := m.testView()
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
	m.tab = graphTab
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
	m := graphModel(80, 24)
	lines := m.graphView(80, 16)
	line := ansi.Strip(lines[len(lines)-1])
	if !strings.HasSuffix(line, "? help") || !strings.Contains(line, "enter card") || strings.Contains(line, "…") || ansi.StringWidth(line) > 80 {
		t.Fatalf("invalid hint line %q", line)
	}
	if !strings.Contains(line, "left/right select") || strings.Contains(line, "space pause") || strings.Contains(line, "o orbit") || strings.Contains(line, "t tools") {
		t.Fatalf("incorrect hint line %q", line)
	}
}
func TestGraphHintsDropInPriorityOrder(t *testing.T) {
	for _, tc := range []struct {
		width           int
		present, absent []string
	}{
		{80, []string{"left/right select", "+/- zoom", "enter card", "? help"}, []string{"0 fit", "o orbit", "t tools", "space pause"}},
		{60, []string{"left/right select", "+/- zoom", "enter card", "? help"}, []string{"0 fit", "o orbit", "t tools"}},
		{40, []string{"left/right select", "+/- zoom", "? help"}, []string{"enter card", "0 fit", "o orbit", "t tools"}},
	} {
		line := graphHintLine(tc.width)
		if ansi.StringWidth(line) > tc.width || strings.Contains(line, "…") || !strings.HasSuffix(line, "? help") {
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
func TestGraphStatusLineNamesSelectedNode(t *testing.T) {
	m := graphModel(120, 34)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-01"})
	lines := m.graphView(120, 20)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "selected: repo project-01 · component · 2 edges") {
		t.Fatalf("status line does not name the selection:\n%s", plain)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 120 {
			t.Fatalf("status line overflows: %q", ansi.Strip(line))
		}
	}
	m80 := graphModel(80, 24)
	m80.graphState, _ = graphview.Update(m80.graphState, graphview.SelectNode{Name: "project-01"})
	plain = ansi.Strip(strings.Join(m80.graphView(80, 16), "\n"))
	if !strings.Contains(plain, "selected: repo project-01 · component · 2 edges") || strings.Contains(plain, "…") {
		t.Fatalf("narrow status line is cut:\n%s", plain)
	}
}

func TestGraphStatusLineFollowsTheCamera(t *testing.T) {
	m := graphModel(120, 34)
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "project-02"})
	m = keys(m, "+", "h", "h")
	lines := m.graphView(120, 20)
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "selected: repo project-02 · component · 2 edges") {
		t.Fatalf("status line changed after zoom and pan:\n%s", strings.Join(lines, "\n"))
	}
}

func TestGraphHelpDescribesNavigation(t *testing.T) {
	m := graphModel(120, 34)
	m = keys(m, "?")
	text := strings.Join(m.helpContent(120), " ")
	for _, want := range []string{"shift+arrows", ", .", "open the selected agent card", "Legend"} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q: %s", want, text)
		}
	}
	for _, gone := range []string{"o orbit", "space pause", "t tools"} {
		if strings.Contains(text, gone) {
			t.Errorf("help still lists %q", gone)
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
	next, _ := m.Update(tea.MouseClickMsg{X: cell.X + 2, Y: cell.Y + 1, Button: tea.MouseLeft})
	m = next.(Model)
	if m.graphState.Graph.Selected() != target {
		t.Fatalf("selected %q, want %q", m.graphState.Graph.Selected(), target)
	}
	next, _ = m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if next.(Model).graphState.Graph.Selected() != target {
		t.Fatal("empty click changed selection")
	}
}

func TestGraphMouseDisabledWhenLeavingOrFocusingCTO(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Text: "1", Code: '1'}, {Text: "C", Code: 'C'}} {
		m := graphModel(120, 34)
		next, _ := m.Update(key)
		if next.(Model).View().MouseMode != tea.MouseModeNone {
			t.Fatalf("%s did not disable mouse", key.String())
		}
	}
}

func TestGraphQuitDisablesMouseBeforeQuit(t *testing.T) {
	m := graphModel(120, 34)
	next, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	if cmd == nil || next.(Model).View().MouseMode != tea.MouseModeNone {
		t.Fatal("quit must release mouse")
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

func TestGraphMockupMouseZoomAndPan(t *testing.T) {
	m := graphModel(120, 34)
	old := m.graphState.Camera
	next, _ := m.Update(tea.MouseWheelMsg{X: 20, Y: 5, Button: tea.MouseWheelUp})
	m = next.(Model)
	if m.graphState.Camera.Zoom <= old.Zoom || m.graphState.Camera.Center == old.Center {
		t.Fatalf("wheel did not zoom at pointer: %+v", m.graphState.Camera)
	}
	old = m.graphState.Camera
	next, _ = m.Update(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
	m = next.(Model)
	next, _ = m.Update(tea.MouseMotionMsg{X: 9, Y: 4, Button: tea.MouseLeft})
	m = next.(Model)
	if m.graphState.Camera.Center == old.Center {
		t.Fatal("drag did not pan")
	}
}

func TestGraphMockupDoubleClickAndTrafficKey(t *testing.T) {
	m := graphModel(120, 34)
	m.graphPixel = &graphPixelState{graphics: true, cellW: 8, cellH: 16, profile: colorprofile.TrueColor}
	m = renderGraphFrame(t, m)
	w, h := m.graphSize()
	scene := m.graphScene(m.graphState, graphview.Size{Width: w, Height: h})
	var target graphview.SceneNode
	for _, n := range scene.Nodes {
		if n.Name != m.graphState.Graph.Selected() && n.Position.X > .1 && n.Position.X < .9 && n.Position.Y > .1 && n.Position.Y < .9 {
			target = n
			break
		}
	}
	if target.Name == "" {
		t.Fatal("no target")
	}
	x, y := int(target.Position.X*float64(w))+2, int(target.Position.Y*float64(h))+1
	for i := 0; i < 2; i++ {
		next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		m = next.(Model)
	}
	if m.graphState.Graph.Selected() != target.Name || m.graphState.Camera.Zoom < m.graphState.Camera.FitZoom*2 {
		t.Fatalf("double click: %s %+v", m.graphState.Graph.Selected(), m.graphState.Camera)
	}
	m = keys(m, "m")
	if !m.graphState.ShowAllTalk {
		t.Fatal("m did not show all traffic")
	}
	m = keys(m, "m")
	if m.graphState.ShowAllTalk {
		t.Fatal("m did not restore selection traffic")
	}
}

func TestGraphMouseOffForSessionForm(t *testing.T) {
	m := graphModel(120, 34)
	m.mode = modeSessionForm
	if m.graphMouseActive() {
		t.Fatal("mouse capture stayed on while the session form replaced the graph")
	}
}

func TestGraphHidesTheCTOBoxWithoutAGraphProposalAt80(t *testing.T) {
	m := graphModel(80, 24)
	view := ansi.Strip(m.testView())
	if strings.Contains(view, "╭─CTO") {
		t.Errorf("the graph kept an empty CTO box:\n%s", view)
	}
	if strings.Contains(view, "no messages yet") {
		t.Errorf("the 80x24 graph kept its feed line:\n%s", view)
	}
	if !strings.Contains(view, "● charter · ○ open · gold: approved · pink: unverified") {
		t.Errorf("the compact legend is missing:\n%s", view)
	}
	m.proposals = []protocol.CTOProposal{{ID: "p1", NodeID: "n_mac", Tool: "graph_change", Action: "charter_set", Status: "open"}}
	if view = ansi.Strip(m.testView()); !strings.Contains(view, "╭─CTO") {
		t.Errorf("the graph proposal did not bring the CTO box back:\n%s", view)
	}
}

func TestGraphCTOBoxStaysOnOtherTabs(t *testing.T) {
	m := graphModel(80, 24)
	m.tab = sessionsTab
	if !strings.Contains(ansi.Strip(m.testView()), "╭─CTO") {
		t.Error("Sessions lost the CTO box")
	}
}
