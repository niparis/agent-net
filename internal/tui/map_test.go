package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeGraph is the map's fixture: two architects with nodes, a component with
// no architect, an architect with no nodes, and histories for the selected
// component.
func fakeGraph() protocol.GraphView {
	return protocol.GraphView{
		Nodes: []protocol.Node{
			{Name: "api-architect", Kind: "architect", Charter: protocol.Charter{Purpose: "API design across projects.", Maturity: "early"}},
			{Name: "cogforge", Kind: "component", Charter: protocol.Charter{
				Purpose:   "Document and evidence API.",
				Maturity:  "early - prototype",
				Accepts:   "Generic endpoints and bug fixes.",
				Refuses:   "Client-specific endpoints.",
				Consumers: "tenebrae, shop",
				Escalate:  "Breaking API changes.",
				Needs:     "MCP: tenebrae-library",
				Notes:     "Deploy through homelab.",
			}},
			{Name: "homelab", Kind: "component", Charter: protocol.Charter{Purpose: "Infrastructure.", Maturity: "growing"}},
			{Name: "shop", Kind: "component", Charter: protocol.Charter{Purpose: "Storefront.", Maturity: "mature"}},
			{Name: "tenebrae", Kind: "component", Charter: protocol.Charter{Purpose: "Consumer of cogforge.", Maturity: "growing"}},
			{Name: "trading-architect", Kind: "architect", Charter: protocol.Charter{Purpose: "Looks after the trading system.", Maturity: "mature - stable"}},
			{Name: "wbg-architect", Kind: "architect", Charter: protocol.Charter{Purpose: "WBG data platform.", Maturity: "growing"}},
		},
		Edges: []protocol.Edge{
			{From: "trading-architect", To: "cogforge", Kind: "governs", Mode: "owner", Rule: "normal"},
			{From: "wbg-architect", To: "tenebrae", Kind: "governs", Mode: "advisor", Rule: "normal"},
			{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "ask"},
			{From: "cogforge", To: "shop", Kind: "depends", Rule: "auto"},
		},
		Changes: []protocol.CharterChange{
			{ID: 3, Node: "cogforge", Attr: "maturity", OldValue: "growing", NewValue: "early - prototype", At: t0.Add(-2 * time.Hour).UnixNano(), By: "you (on Mac)"},
			{ID: 2, Node: "cogforge", Attr: "purpose", OldValue: "", NewValue: "Document and evidence API.", At: t0.Add(-26 * time.Hour).UnixNano(), By: "owner"},
			{ID: 1, Node: "tenebrae", Attr: "maturity", OldValue: "early", NewValue: "growing", At: t0.Add(-3 * time.Hour).UnixNano(), By: "trading-architect"},
		},
	}
}

func mapModel(w, h int, graph protocol.GraphView) Model {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), graph: graph}
	src.usage, src.counts, src.tops = fakeUsage(), fakeCounts(), fakeTops()
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

// openMap presses M and runs the graph read it starts.
func openMap(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M")})
	m = next.(Model)
	if !m.mapOn {
		t.Fatal("M did not open the map")
	}
	if cmd == nil {
		t.Fatal("M did not ask for the graph")
	}
	return send(m, cmd())
}

func TestMapGroupsNodesByArchitect(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	m = keys(m, "z")
	for _, want := range []string{"Overview", "trading-architect", "cogforge", "wbg-architect", "tenebrae", "shop", "homelab", "governs", "depends", "Isolated"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("overview missing %q", want)
		}
	}
}

func TestMapShowsTheSelectedNodesCharterEdgesAndHistory(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	m.mp.selected = "cogforge"
	for _, want := range []string{"cogforge", "trading-architect", "tenebrae", "shop", "purpose", "Document and evidence API.", "accepts", "refuses", "governed by", "owner", "depends", "ask", "auto", "RECENT"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("focus missing %q", want)
		}
	}
}

func TestMapTogglesWithTheSessionList(t *testing.T) {
	m := openMap(t, mapModel(100, 30, fakeGraph()))
	m = keys(m, "M")
	if m.mapOn {
		t.Fatal("M again did not close the map")
	}
	if !strings.Contains(m.View(), "NAME") {
		t.Fatalf("the session list did not come back:\n%s", m.View())
	}
	m = openMap(t, m)
	if !strings.Contains(m.View(), "nodes, ") {
		t.Fatalf("M did not open the map again:\n%s", m.View())
	}
	m = keys(m, "esc")
	if m.mapOn || !strings.Contains(m.View(), "NAME") {
		t.Fatal("esc did not close the map")
	}
}

func TestMapExplainsAnEmptyGraphAndAReadFailure(t *testing.T) {
	m := openMap(t, mapModel(100, 30, protocol.GraphView{}))
	if v := m.View(); !strings.Contains(v, "no nodes yet") || !strings.Contains(v, "no node selected") {
		t.Fatalf("an empty graph is not explained:\n%s", v)
	}

	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), graphErr: errors.New("the gateway is too old")}
	m = New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	m = openMap(t, m)
	if v := m.View(); !strings.Contains(v, "could not read the graph") || !strings.Contains(v, "the gateway is too old") {
		t.Fatalf("a read failure is not explained:\n%s", v)
	}
}

func TestMapKeysAreReadOnlyAndMoveTheSelection(t *testing.T) {
	m := openMap(t, mapModel(100, 30, fakeGraph()))
	before := m.mapSelectedName()
	m = keys(m, "left")
	if m.mapSelectedName() == before {
		t.Fatal("left did not move the map selection")
	}
	m = keys(m, "g")
	if m.mapSelectedName() != "api-architect" {
		t.Fatalf("g selected %q", m.mapSelectedName())
	}
	m = keys(m, "G")
	if m.mapSelectedName() != "tenebrae" {
		t.Fatalf("G selected %q", m.mapSelectedName())
	}
	// Keys that act on sessions do nothing on the map.
	if m.mode != 0 || m.confirmID != "" {
		t.Fatal("a session action started from the map")
	}
}

func TestGoldenMap(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size[0], size[1], fakeGraph()))
		m.mp.selected = "cogforge"
		golden(t, fmt.Sprintf("map_focus_%dx%d", size[0], size[1]), m.View())
		m = keys(m, "z")
		golden(t, fmt.Sprintf("map_overview_%dx%d", size[0], size[1]), m.View())
	}
}

func TestMapIsFourthTab(t *testing.T) {
	m := mapModel(120, 34, fakeGraph())
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = next.(Model)
	if !m.mapOn || m.tab != 3 || cmd == nil {
		t.Fatal("4 did not open Map")
	}
	m = send(m, cmd())
	if !strings.Contains(m.View(), "4 Map") || !strings.Contains(m.View(), "CTO") {
		t.Fatal(m.View())
	}
	m = keys(m, "2")
	if m.mapOn || m.tab != 1 {
		t.Fatal("2 did not leave Map")
	}
}

func TestMapFocusNavigationAndActions(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	v := m.View()
	for _, want := range []string{"cogforge", "trading-architect", "tenebrae", "shop", "depends", "ask", "auto", "purpose", "accepts", "refuses"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q", want)
		}
	}
	m = keys(m, "left")
	if m.mp.selected != "tenebrae" {
		t.Fatalf("left: %s", m.mp.selected)
	}
	m.mp.selected = "cogforge"
	m = keys(m, "right")
	if m.mp.selected != "shop" {
		t.Fatalf("right: %s", m.mp.selected)
	}
	m.mp.selected = "cogforge"
	m = keys(m, "c")
	if m.focus != focusCTO || m.cto.input.Value() != "about cogforge: " {
		t.Fatal("CTO prompt missing")
	}
	m = keys(m, "tab", "enter")
	if m.mapOn || m.tab != 0 || m.filter != "cogforge" {
		t.Fatal("project filter missing")
	}
}

func TestMapOverviewFitsNarrowTerminal(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	m = keys(m, "z")
	if !m.mp.overview || !strings.Contains(m.View(), "Overview") || !strings.Contains(m.View(), "trading-architect") {
		t.Fatalf("overview missing:\n%s", m.View())
	}
	m.width = 50
	if strings.Contains(m.View(), "overview does not fit") || !strings.Contains(m.View(), "Overview") {
		t.Fatal("overview missing at narrow width")
	}
}

func TestMapDefaultsToProject(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	n, ok := m.mapNode(m.mp.selected)
	if !ok || n.Kind == "architect" {
		t.Fatalf("default focus is not a project: %q", m.mp.selected)
	}
}

func TestMapHelpAndHints(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	line := m.helpLine(120)
	for _, want := range []string{"left/right", "z", "enter", "c"} {
		if !strings.Contains(line, want) {
			t.Errorf("map hints missing %q", want)
		}
	}
	if !strings.Contains(keys(m, "?").View(), "overview") {
		t.Fatal("map help omits overview")
	}
}

func TestMapHidesAndUnhidesNodesWithSharedPreference(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), graph: fakeGraph(),
		hidden: protocol.HiddenItems{Nodes: []string{"cogforge"}}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	m = openMap(t, m)
	if containsString(m.mapSelectable(), "cogforge") || !strings.Contains(m.mapTitle(120), "hidden 1") {
		t.Fatalf("hidden node is still listed or not counted:\n%s", m.View())
	}
	m = keys(m, "H")
	if !containsString(m.mapSelectable(), "cogforge") || !strings.Contains(m.View(), "hidden") {
		t.Fatalf("X did not show the tagged hidden node:\n%s", m.View())
	}
	m.mp.selected = "cogforge"
	m = finishHiddenKey(t, m, "h")
	if containsString(src.hidden.Nodes, "cogforge") || !containsString(m.mapSelectable(), "cogforge") {
		t.Fatalf("hidden node was not unhidden: %+v / %v", src.hidden, m.mapSelectable())
	}
	m = keys(m, "H")
	m.mp.selected = "shop"
	m = finishHiddenKey(t, m, "h")
	if !containsString(src.hidden.Nodes, "shop") || containsString(m.mapSelectable(), "shop") {
		t.Fatalf("visible node was not hidden: %+v / %v", src.hidden, m.mapSelectable())
	}
	line := m.helpLine(120)
	if !strings.Contains(line, "h hide") || !strings.Contains(line, "H hidden") {
		t.Fatalf("map key hints: %s", line)
	}
}

func TestMapFocusKeepsArchitectNameAt80(t *testing.T) {
	m := openMap(t, mapModel(80, 24, fakeGraph()))
	m.mp.selected = "cogforge"
	if !strings.Contains(m.View(), "trading-architect") {
		t.Fatal("architect name was cut")
	}
}

func TestOverviewConnectsNodes(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	m = keys(m, "z")
	v := m.View()
	if !strings.Contains(v, "┄┄") || !strings.Contains(v, "━━") {
		t.Fatalf("missing connected edge routes:\n%s", v)
	}
}

func TestFocusShowsMultipleNeighborsWhenThereIsRoom(t *testing.T) {
	g := fakeGraph()
	g.Edges = append(g.Edges, protocol.Edge{From: "homelab", To: "cogforge", Kind: "depends", Rule: "normal"})
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "cogforge"
	if !strings.Contains(m.View(), "homelab") || !strings.Contains(m.View(), "tenebrae") {
		t.Fatal("focus omits a neighbor")
	}
}

func TestMapReadFailureKeepsLastGraphVisible(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	m.applyMap(mapMsg{err: errors.New("temporary failure")})
	if v := m.View(); !strings.Contains(v, "cogforge") || !strings.Contains(v, "temporary failure") {
		t.Fatalf("last graph or error missing:\n%s", v)
	}
}

func TestFocusCountsAppearOnlyOnTabBar(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	v := ansi.Strip(m.View())
	ls := strings.Split(v, "\n")
	if !strings.Contains(ls[0], "7 nodes, 4 edges") {
		t.Fatalf("counts missing on tab bar: %q", ls[0])
	}
	if strings.Contains(v, "Map (read only)") || strings.Count(v, "updated ") != 1 {
		t.Fatalf("duplicate header: %s", v)
	}
}
func TestMatureUsesDistinctBlue(t *testing.T) {
	if mapLevelStyle("mature").GetForeground() != roleMature || roleMature == roleAccent {
		t.Fatal("mature should use blue")
	}
}
func TestOverviewRoutesEachDependencyInItsOwnLane(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	m.mp.overview = true
	v := ansi.Strip(m.View())
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "┄┄") && strings.Contains(line, "━━") {
			t.Fatalf("edges share a lane: %q", line)
		}
	}
}

func TestMapListMovesFromIsolatedNode(t *testing.T) {
	g := fakeGraph()
	g.Nodes = append(g.Nodes, protocol.Node{Name: "a2a-ocs", Kind: "component"})
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "a2a-ocs"
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "▸ a2a-ocs") || !strings.Contains(v, "cogforge") {
		t.Fatal(v)
	}
	m = keys(m, "down")
	if m.mp.selected != "cogforge" {
		t.Fatalf("down from isolated selected %q", m.mp.selected)
	}
}

func TestMapFindFiltersAndConfirmsOrCancels(t *testing.T) {
	g := fakeGraph()
	g.Nodes = append(g.Nodes, protocol.Node{Name: "a2a-ocs", Kind: "component"})
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "a2a-ocs"
	m = keys(m, "/", "c", "o", "g")
	if m.mp.selected != "a2a-ocs" || len(m.mapSelectable()) != 1 || m.mapSelectable()[0] != "cogforge" {
		t.Fatalf("find: %s %v", m.mp.selected, m.mapSelectable())
	}
	if !strings.Contains(ansi.Strip(m.View()), "find: cog") {
		t.Fatal("find prompt missing")
	}
	m = keys(m, "enter")
	if m.mp.selected != "cogforge" || len(m.mapSelectable()) < 2 {
		t.Fatal("find did not focus and restore list")
	}
	m = keys(m, "/", "s", "h", "esc")
	if m.mp.selected != "cogforge" || len(m.mapSelectable()) < 2 {
		t.Fatal("cancel changed selection")
	}
}

func TestMapInitialFocusPrefersChangedConnectedNode(t *testing.T) {
	g := fakeGraph()
	g.Nodes = append(g.Nodes, protocol.Node{Name: "a2a-ocs", Kind: "component"})
	g.Changes = append(g.Changes, protocol.CharterChange{Node: "a2a-ocs", Attr: "purpose", At: t0.UnixNano()})
	m := openMap(t, mapModel(120, 34, g))
	if m.mp.selected != "cogforge" {
		t.Fatalf("initial selected %q", m.mp.selected)
	}
	m.mp.selected = "shop"
	m = keys(m, "1")
	m = openMap(t, m)
	if m.mp.selected != "shop" {
		t.Fatalf("selection lost: %q", m.mp.selected)
	}
	g.Changes = nil
	m = openMap(t, mapModel(120, 34, g))
	if m.mp.selected != "cogforge" {
		t.Fatalf("most connected: %q", m.mp.selected)
	}
	g.Edges = nil
	m = openMap(t, mapModel(120, 34, g))
	if m.mp.selected != "api-architect" {
		t.Fatalf("first node: %q", m.mp.selected)
	}
}

func TestMapExplainsIsolatedNode(t *testing.T) {
	g := fakeGraph()
	g.Nodes = append(g.Nodes, protocol.Node{Name: "a2a-ocs", Kind: "component"})
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "a2a-ocs"
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "no edges yet · ask the CTO to connect it (c)") {
		t.Fatal(v)
	}
}

func TestMapCompactBoxesKeepMaturityAndSessionCount(t *testing.T) {
	m := openMap(t, mapModel(80, 24, fakeGraph()))
	m.mp.selected = "cogforge"
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "cogforge  early") || !strings.Contains(v, "no live sessions") {
		t.Fatal(v)
	}
}

func TestGoldenMapNavigation(t *testing.T) {
	graph := protocol.GraphView{Nodes: []protocol.Node{{Name: "architect", Kind: "architect", Charter: protocol.Charter{Purpose: "Coordinate projects."}}, {Name: "a2a-ocs", Kind: "component"}, {Name: "cogforge", Kind: "component", Charter: protocol.Charter{Purpose: "Document and evidence API.", Maturity: "growing", Accepts: "Generic endpoints.", Refuses: "Client-specific work."}}, {Name: "tenebrae", Kind: "component", Charter: protocol.Charter{Maturity: "early"}}, {Name: "shop", Kind: "component", Charter: protocol.Charter{Maturity: "mature"}}}, Edges: []protocol.Edge{{From: "architect", To: "cogforge", Kind: "governs", Mode: "owner", Rule: "normal"}, {From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "ask"}, {From: "cogforge", To: "shop", Kind: "depends", Rule: "auto"}}, Changes: []protocol.CharterChange{{Node: "cogforge", Attr: "purpose", NewValue: "Document and evidence API.", At: t0.UnixNano()}}}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size[0], size[1], graph))
		golden(t, fmt.Sprintf("map_navigation_%dx%d", size[0], size[1]), m.View())
		m.mp.selected = "a2a-ocs"
		golden(t, fmt.Sprintf("map_isolated_%dx%d", size[0], size[1]), m.View())
		m = keys(m, "/", "c", "o", "g")
		golden(t, fmt.Sprintf("map_find_%dx%d", size[0], size[1]), m.View())
	}
}

func TestLargeOverviewNeverFallsBack(t *testing.T) {
	g := fakeGraph()
	for i := 0; i < 25; i++ {
		g.Nodes = append(g.Nodes, protocol.Node{Name: fmt.Sprintf("project-%02d", i), Kind: "component"})
	}
	m := openMap(t, mapModel(80, 24, g))
	m = keys(m, "z")
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Overview") || strings.Contains(v, "overview does not fit") {
		t.Fatal(v)
	}
}

func TestOverviewNavigationAndFocus(t *testing.T) {
	g := fakeGraph()
	m := openMap(t, mapModel(80, 24, g))
	m.mp.selected = "cogforge"
	m = keys(m, "z", "right")
	if !m.mp.overview || m.mp.selected == "cogforge" {
		t.Fatalf("right did not select a box: %q", m.mp.selected)
	}
	selected := m.mp.selected
	m = keys(m, "enter")
	if !m.mapOn || m.mp.overview || m.mp.selected != selected {
		t.Fatal("enter did not return to focus")
	}
}
func TestOverviewScrollKeepsEdgesWithinVisibleBoxes(t *testing.T) {
	g := fakeGraph()
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("node-%02d", i)
		g.Nodes = append(g.Nodes, protocol.Node{Name: name, Kind: "component"})
		g.Edges = append(g.Edges, protocol.Edge{From: "trading-architect", To: name, Kind: "governs"})
	}
	m := openMap(t, mapModel(80, 24, g))
	m = keys(m, "z")
	before := ansi.Strip(m.View())
	if !strings.Contains(before, "edges off screen") {
		t.Fatal(before)
	}
	m = keys(m, "pgdown")
	after := ansi.Strip(m.View())
	if (m.mp.overviewScroll == 0 && m.mp.overviewEdgeScroll == 0) || before == after {
		t.Fatal("page did not scroll")
	}
}

func TestOverviewDownScrollsPastLastBoxToIsolated(t *testing.T) {
	g := fakeGraph()
	for i := 0; i < 50; i++ {
		g.Nodes = append(g.Nodes, protocol.Node{Name: fmt.Sprintf("isolated-%02d", i), Kind: "component"})
	}
	m := openMap(t, mapModel(80, 24, g))
	m = keys(m, "z")
	for i := 0; i < 60; i++ {
		m = keys(m, "down")
	}
	if m.mp.overviewScroll == 0 || !strings.Contains(m.View(), "isolated-") {
		t.Fatal("down did not scroll to isolated projects")
	}
}

func TestOverviewOnlyDrawsEdgesBetweenVisibleBoxes(t *testing.T) {
	g := protocol.GraphView{}
	for i := 0; i < 30; i++ {
		g.Nodes = append(g.Nodes, protocol.Node{Name: fmt.Sprintf("node-%02d", i), Kind: "component"})
		if i > 0 {
			g.Edges = append(g.Edges, protocol.Edge{From: fmt.Sprintf("node-%02d", i-1), To: fmt.Sprintf("node-%02d", i), Kind: "depends", Rule: "auto"})
		}
	}
	m := openMap(t, mapModel(80, 24, g))
	m = keys(m, "z", "pgdown")
	out, _ := m.mapOverviewView(80, m.overviewHeight())
	text := ansi.Strip(strings.Join(out, "\n"))
	if !strings.Contains(text, "edges off screen") {
		t.Fatal(text)
	}
	visible := map[string]bool{}
	for _, line := range out {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "[") {
			for _, n := range g.Nodes {
				if strings.Contains(plain, n.Name) {
					visible[n.Name] = true
				}
			}
		}
	}
	for _, e := range g.Edges {
		if strings.Contains(text, e.From+" ━━━▶ "+e.To) && (!visible[e.From] || !visible[e.To]) {
			t.Fatalf("edge has hidden endpoint: %+v\n%s", e, text)
		}
	}
}

func largeOverviewGraph() protocol.GraphView {
	g := protocol.GraphView{}
	for i := 0; i < 3; i++ {
		g.Nodes = append(g.Nodes, protocol.Node{Name: fmt.Sprintf("architect-%d", i+1), Kind: "architect", Charter: protocol.Charter{Maturity: "mature"}})
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("project-%02d", i+1)
		g.Nodes = append(g.Nodes, protocol.Node{Name: name, Kind: "component", Charter: protocol.Charter{Maturity: "growing"}})
		rule := "normal"
		if i%3 == 0 {
			rule = "auto"
		} else if i%3 == 1 {
			rule = "ask"
		}
		g.Edges = append(g.Edges, protocol.Edge{From: fmt.Sprintf("architect-%d", i%3+1), To: name, Kind: "governs", Rule: rule})
	}
	for i := 0; i < 12; i++ {
		g.Nodes = append(g.Nodes, protocol.Node{Name: fmt.Sprintf("isolated-%02d", i+1), Kind: "component"})
	}
	g.Changes = []protocol.CharterChange{{Node: "project-01", Attr: "maturity", At: t0.UnixNano()}}
	return g
}
func TestGoldenLargeOverview(t *testing.T) {
	g := largeOverviewGraph()
	if len(g.Nodes) != 25 || len(g.Edges) != 10 {
		t.Fatal("bad fixture")
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := openMap(t, mapModel(size[0], size[1], g))
		m = keys(m, "z")
		v := m.View()
		for _, line := range lines(v) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("wide line: %s", line)
			}
		}
		if strings.Contains(v, "overview does not fit") || !strings.Contains(v, "Overview") {
			t.Fatal(v)
		}
		golden(t, fmt.Sprintf("map_large_overview_%dx%d", size[0], size[1]), v)
		m = keys(m, "pgdown", "pgdown", "pgdown", "pgdown")
		golden(t, fmt.Sprintf("map_large_overview_scrolled_%dx%d", size[0], size[1]), m.View())
	}
}

func TestOverviewCompactBoxIsVisuallyClosed(t *testing.T) {
	m := openMap(t, mapModel(80, 24, fakeGraph()))
	n, _ := m.mapNode("cogforge")
	s := ansi.Strip(m.overviewBox(n, 32))
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		t.Fatal(s)
	}
}

func TestOverviewIsolatedProjectsFollowEdges(t *testing.T) {
	m := openMap(t, mapModel(120, 34, largeOverviewGraph()))
	m = keys(m, "z")
	v := ansi.Strip(m.View())
	if strings.Index(v, "Isolated:") < strings.Index(v, "governs auto") {
		t.Fatal(v)
	}
}

func TestOverviewCanPageThroughVisibleEdges(t *testing.T) {
	m := openMap(t, mapModel(80, 24, largeOverviewGraph()))
	m = keys(m, "z")
	before := ansi.Strip(m.View())
	if !strings.Contains(before, "architect-1 ━━━▶ project-01") || strings.Contains(before, "architect-1 ━━━▶ project-04") {
		t.Fatal(before)
	}
	m = keys(m, "pgdown")
	after := ansi.Strip(m.View())
	if !strings.Contains(after, "architect-1 ━━━▶ project-04") {
		t.Fatalf("next visible edges cannot be reached:\n%s", after)
	}
}

func TestMapDetailShowsProjectContext(t *testing.T) {
	node := protocol.Node{Name: "p", Kind: "component", Locations: []protocol.GraphLocation{{Machine: "box", Path: "/src/p"}}, Sessions: []protocol.GraphSession{{ID: "s1", Name: "builder", Machine: "box", Status: "busy"}}, SessionCount: 6}
	m := mapModel(120, 40, protocol.GraphView{})
	got := strings.Join(m.mapDetailLines(node, true, 100, 40), "\n")
	for _, want := range []string{"/src/p", "builder", "s1", "busy", "5 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}
