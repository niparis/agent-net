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

func TestFitUsesViewportInBothDimensions(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{})
	g.Positions = map[string]Point{"a": {-4, -1}, "b": {4, 1}}
	for _, size := range [][2]int{{100, 20}, {40, 30}} {
		c := g.Fit(size[0], size[1])
		x0, y0 := c.Project(g.Positions["a"], size[0], size[1])
		x1, y1 := c.Project(g.Positions["b"], size[0], size[1])
		if x1-x0 < float64(size[0]-4) || y1-y0 < float64(size[1]-4) {
			t.Errorf("fit in %v uses %.1fx%.1f", size, x1-x0, y1-y0)
		}
	}
}
