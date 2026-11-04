package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

// openMap opens the network's selected agent card for card tests.
func openMap(t *testing.T, m Model) Model {
	t.Helper()
	m.applyMap(mapMsg{view: m.src.(*fakeSource).graph, at: t0})
	m.tab, m.mapOn, m.focus = graphTab, true, focusSessions
	m.mp.loading = false
	m.uiFilter = ""
	return m
}

func TestMapHidesAndUnhidesNodesWithSharedPreference(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), graph: fakeGraph(),
		hidden: protocol.HiddenItems{Nodes: []string{"cogforge"}}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	m = openMap(t, m)
	if containsString(m.mapSelectable(), "cogforge") || len(m.hiddenNodes) != 1 {
		t.Fatalf("hidden node is still listed or not counted:\n%s", m.testView())
	}
	m = keys(m, "H")
	if !containsString(m.mapSelectable(), "cogforge") || !m.showHiddenNodes {
		t.Fatalf("X did not show the tagged hidden node:\n%s", m.testView())
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
	// The saved notice takes the key line until a key dismisses it; the hints
	// then come back.
	m = keys(m, "down")
	line := ansi.Strip(m.helpLine(120))
	if !strings.Contains(line, "esc Graph") || !strings.Contains(line, "? help") {
		t.Fatalf("map key hints: %s", line)
	}
	if help := ansi.Strip(strings.Join(m.helpContent(120), "\n")); !strings.Contains(help, "hide or unhide") {
		t.Fatalf("map help does not list the hide key: %s", help)
	}
}

func TestMapFocusKeepsArchitectNameAt80(t *testing.T) {
	m := openMap(t, mapModel(80, 24, fakeGraph()))
	m.mp.selected = "cogforge"
	if !strings.Contains(m.testView(), "trading-architect") {
		t.Fatal("architect name was cut")
	}
}

func TestFocusShowsMultipleNeighborsWhenThereIsRoom(t *testing.T) {
	g := fakeGraph()
	g.Edges = append(g.Edges, protocol.Edge{From: "homelab", To: "cogforge", Kind: "depends", Rule: "normal"})
	m := openMap(t, mapModel(120, 34, g))
	m.mp.selected = "cogforge"
	if !strings.Contains(m.testView(), "homelab") || !strings.Contains(m.testView(), "tenebrae") {
		t.Fatal("focus omits a neighbor")
	}
}

func TestMapReadFailureKeepsLastGraphVisible(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	m.applyMap(mapMsg{err: errors.New("temporary failure")})
	if v := m.testView(); !strings.Contains(v, "cogforge") || !strings.Contains(v, "temporary failure") {
		t.Fatalf("last graph or error missing:\n%s", v)
	}
}

func TestFocusCountsAppearOnlyOnTabBar(t *testing.T) {
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.mp.selected = "cogforge"
	v := ansi.Strip(m.testView())
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
	v := ansi.Strip(m.testView())
	if !strings.Contains(v, "no edges yet · ask the CTO to connect it (c)") {
		t.Fatal(v)
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
