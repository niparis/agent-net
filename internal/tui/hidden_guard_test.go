package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's own session is never hidden: hiding it would remove the only
// way back into the TUI's owner chat.
func TestOwnerSessionCannotBeHidden(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	m := hiddenModel(120, 34, src)
	m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "s_owner", Harness: protocol.OwnerHarness, Online: true})
	m.selected = "s_owner"
	if _, cmd := m.toggleHiddenSession(); cmd != nil {
		t.Fatal("the owner session started a hidden-items update")
	}
	// A normal session does.
	m.selected = "s_0000000000000003"
	if _, cmd := m.toggleHiddenSession(); cmd == nil {
		t.Fatal("a normal session did not start a hidden-items update")
	}
}

// A name that is not a node of the map is not added to the shared preference.
func TestUnknownMapNodeCannotBeHidden(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), graph: fakeGraph()}
	m := openMap(t, mapModel(120, 34, fakeGraph()))
	m.src = src
	m.mp.selected = "no-such-node"
	if _, cmd := m.toggleHiddenNode(); cmd != nil {
		t.Fatal("an unknown node started a hidden-items update")
	}
	m.mp.selected = "shop"
	if _, cmd := m.toggleHiddenNode(); cmd == nil {
		t.Fatal("a real node did not start a hidden-items update")
	}
}

// An edge that touches a hidden node, at either end, leaves the map with it.
func TestHiddenNodeTakesItsEdgesAtBothEnds(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	g := fakeGraph()
	m.mp.nodes, m.mp.edges = g.Nodes, g.Edges
	m.hiddenNodes = map[string]bool{"cogforge": true}
	edges := m.mapVisibleEdges()
	if len(edges) != 1 || edges[0].From != "wbg-architect" || edges[0].To != "tenebrae" {
		t.Fatalf("edges left with cogforge hidden: %+v", edges)
	}
	// Hidden as the source of an edge only.
	m.hiddenNodes = map[string]bool{"wbg-architect": true}
	if n := len(m.mapVisibleEdges()); n != 3 {
		t.Fatalf("edges left with a source hidden: %d", n)
	}
	// Hidden as the target of an edge only.
	m.hiddenNodes = map[string]bool{"shop": true}
	if n := len(m.mapVisibleEdges()); n != 3 {
		t.Fatalf("edges left with a target hidden: %d", n)
	}
	m.showHiddenNodes = true
	if n := len(m.mapVisibleEdges()); n != 4 {
		t.Fatalf("show hidden did not bring the edges back: %d", n)
	}
}
