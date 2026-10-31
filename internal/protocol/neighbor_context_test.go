package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestNeighborCharters(t *testing.T) {
	g := Graph{Nodes: []Node{{Name: "app"}, {Name: "ops", Charter: Charter{Accepts: "deploys", Refuses: "manual changes"}}}, Edges: []Edge{{From: "ops", To: "app", Kind: "deploys"}}}
	want := "ops: accepts deploys; refuses manual changes; ask it for those"
	if got := NeighborCharters(g, "app"); !strings.Contains(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := NeighborCharters(g, "other"); got != "" {
		t.Fatalf("unexpected %q", got)
	}
	if got := NodeContext(g, "app"); !strings.Contains(got, want) {
		t.Fatalf("context missing neighbor: %q", got)
	}
}

func TestNeighborCharterCaps(t *testing.T) {
	g := Graph{Nodes: []Node{{Name: "app"}}}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("node%02d", i)
		g.Nodes = append(g.Nodes, Node{Name: name, Charter: Charter{Accepts: strings.Repeat("a", 500), Refuses: strings.Repeat("b", 500)}})
		g.Edges = append(g.Edges, Edge{From: "app", To: name, Kind: "depends"})
	}
	got := NeighborCharters(g, "app")
	if len(got) > 1500 || strings.Count(got, "ask it for those") != 8 {
		t.Fatalf("length %d lines %d", len(got), strings.Count(got, "ask it for those"))
	}
	for _, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > 160 {
			t.Fatalf("long line: %d", len([]rune(line)))
		}
	}
}
