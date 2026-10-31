package graphview

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func fixture() protocol.GraphView {
	return protocol.GraphView{Nodes: []protocol.Node{{Name: "b"}, {Name: "a"}, {Name: "c"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: "depends"}, {From: "*", To: "c", Kind: "governs"}}}
}
func TestLayoutDeterministicAndFit(t *testing.T) {
	a, b := New(fixture()), New(fixture())
	if !reflect.DeepEqual(a.Positions, b.Positions) {
		t.Fatal("layout differs")
	}
	c := a.Fit(80, 24)
	for name, p := range a.Positions {
		x, y := c.Project(p, 80, 24)
		if x < 0 || x >= 80 || y < 0 || y >= 24 {
			t.Fatalf("%s outside fit: %v %v", name, x, y)
		}
	}
}
func TestSelectionStable(t *testing.T) {
	g := New(fixture())
	want := []string{"a", "b", "c", "a"}
	for _, v := range want {
		if g.Selected() != v {
			t.Fatalf("selected %q want %q", g.Selected(), v)
		}
		g.Select(1)
	}
}
