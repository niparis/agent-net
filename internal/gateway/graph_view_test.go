package gateway

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// graphTestStore creates a store with three projects, one architect and two
// edges, and returns it.
func graphTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, err = s.AddProjects("n_test", protocol.ProjectAdd{Projects: []protocol.ProjectEntry{
		{Name: "cogforge", Path: "/tmp/cogforge"},
		{Name: "tenebrae", Path: "/tmp/tenebrae"},
		{Name: "shop", Path: "/tmp/shop"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("trading-architect", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ node, attr, value string }{
		{"trading-architect", "maturity", "mature - stable"},
		{"cogforge", "purpose", "Generic document API"},
		{"cogforge", "maturity", "early - prototype"},
		{"tenebrae", "maturity", "growing"},
	} {
		if err := s.SetCharter(c.node, c.attr, c.value, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []protocol.Edge{
		{From: "trading-architect", To: "cogforge", Kind: "governs", Mode: "owner", Rule: "normal"},
		{From: "tenebrae", To: "cogforge", Kind: "depends", Rule: "ask"},
	} {
		if err := s.SetEdge(e, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestGraphViewReturnsNodesChartersEdgesAndHistory(t *testing.T) {
	s := graphTestStore(t)
	view, err := s.GraphView("", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 4 {
		t.Fatalf("%d nodes, want 4: %#v", len(view.Nodes), view.Nodes)
	}
	if len(view.Edges) != 2 {
		t.Fatalf("%d edges, want 2: %#v", len(view.Edges), view.Edges)
	}
	byName := map[string]protocol.Node{}
	for _, n := range view.Nodes {
		byName[n.Name] = n
	}
	if got := byName["cogforge"].Charter.Purpose; got != "Generic document API" {
		t.Fatalf("cogforge purpose %q", got)
	}
	if got := byName["cogforge"].Charter.Maturity; got != "early - prototype" {
		t.Fatalf("cogforge maturity %q", got)
	}
	if len(view.Changes) != 4 {
		t.Fatalf("%d changes, want 4: %#v", len(view.Changes), view.Changes)
	}
	if view.Changes[0].Attr != "maturity" || view.Changes[0].Node != "tenebrae" {
		t.Fatalf("changes are not newest first: %#v", view.Changes)
	}
}

func TestGraphViewKeepsTheLastChangesPerNode(t *testing.T) {
	s := graphTestStore(t)
	for i := 0; i < 7; i++ {
		if err := s.SetCharter("cogforge", "notes", fmt.Sprintf("note %d", i), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.GraphView("", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, c := range view.Changes {
		if c.Node == "cogforge" && c.Attr == "notes" {
			got++
		}
	}
	if want := protocol.GraphHistoryPerNode; got != want {
		t.Fatalf("%d notes changes for cogforge, want %d (the newest)", got, want)
	}
}

func TestGraphViewOfOneProjectIsItsNeighbourhood(t *testing.T) {
	s := graphTestStore(t)
	view, err := s.GraphView("cogforge", protocol.GraphHistoryPerNode)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range view.Nodes {
		names[n.Name] = true
	}
	for _, want := range []string{"cogforge", "trading-architect", "tenebrae"} {
		if !names[want] {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	if names["shop"] {
		t.Error("shop has no edge with cogforge and must not be there")
	}
	if len(view.Edges) != 2 {
		t.Fatalf("%d edges, want the 2 that touch cogforge: %#v", len(view.Edges), view.Edges)
	}
	for _, c := range view.Changes {
		if c.Node == "shop" {
			t.Fatalf("a change of an unrelated node: %#v", c)
		}
	}
}
