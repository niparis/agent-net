package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	v := m.View()
	for _, want := range []string{
		"Map (read only)",
		"trading-architect", "cogforge", "[owner]",
		"wbg-architect", "tenebrae", "[advisor]",
		"Components with no architect", "shop",
		"Architects without nodes", "api-architect",
		"early", "growing", "mature",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the map lacks %q:\n%s", want, v)
		}
	}
	// The three groups come in this order.
	at := func(s string) int { return strings.Index(v, s) }
	if !(at("trading-architect") < at("Components with no architect") &&
		at("Components with no architect") < at("Architects without nodes")) {
		t.Errorf("the groups are not in the order architect, ungoverned, empty architect:\n%s", v)
	}
}

func TestMapShowsTheSelectedNodesCharterEdgesAndHistory(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	m = keys(m, "down") // trading-architect -> cogforge
	if m.mapSelectedName() != "cogforge" {
		t.Fatalf("selected %q, want cogforge", m.mapSelectedName())
	}
	v := m.View()
	for _, want := range []string{
		"Charter", "purpose", "Document and evidence API.",
		"maturity", "early - prototype",
		"accepts", "Generic endpoints and bug fixes.",
		"refuses", "Client-specific endpoints.",
		"consumers", "tenebrae, shop",
		"escalate", "Breaking API changes.",
		"needs", "MCP: tenebrae-library",
		"notes", "Deploy through homelab.",
		"Edges", "trading-architect -> cogforge", "governs", "owner",
		"tenebrae -> cogforge", "ask", "cogforge -> shop", "auto",
		"Last changes", "you (on Mac)", "maturity", "purpose",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the detail lacks %q:\n%s", want, v)
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
	if !strings.Contains(m.View(), "Map (read only)") {
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
	m = keys(m, "down", "down")
	if m.mapSelectedName() == before {
		t.Fatal("down did not move the map selection")
	}
	m = keys(m, "g")
	if m.mapSelectedName() != "trading-architect" {
		t.Fatalf("g selected %q", m.mapSelectedName())
	}
	m = keys(m, "G")
	if m.mapSelectedName() != "api-architect" {
		t.Fatalf("G selected %q", m.mapSelectedName())
	}
	// Keys that act on sessions do nothing on the map.
	if m.mode != 0 || m.confirmID != "" || m.conv != nil {
		t.Fatal("a session action started from the map")
	}
}

func TestHelpListsTheMapKey(t *testing.T) {
	v := keys(newModel(100, 40), "?").View()
	for _, want := range []string{"map:", "charters", "read only"} {
		if !strings.Contains(v, want) {
			t.Errorf("the help lacks %q:\n%s", want, v)
		}
	}
}

func TestGoldenMap(t *testing.T) {
	m := openMap(t, mapModel(80, 24, fakeGraph()))
	m = keys(m, "down")
	golden(t, "map_80x24", m.View())

	m = openMap(t, mapModel(120, 40, fakeGraph()))
	m = keys(m, "down")
	golden(t, "map_120x40", m.View())
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
  if !strings.Contains(v, want) { t.Errorf("missing %q", want) }
 }
 m = keys(m, "left")
 if m.mp.selected != "tenebrae" { t.Fatalf("left: %s", m.mp.selected) }
 m.mp.selected = "cogforge"
 m = keys(m, "right")
 if m.mp.selected != "shop" { t.Fatalf("right: %s", m.mp.selected) }
 m.mp.selected = "cogforge"
 m = keys(m, "c")
 if m.focus != focusCTO || m.cto.input.Value() != "about cogforge: " { t.Fatal("CTO prompt missing") }
 m = keys(m, "tab", "enter")
 if m.mapOn || m.tab != 0 || m.filter != "cogforge" { t.Fatal("project filter missing") }
}
