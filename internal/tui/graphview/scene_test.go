package graphview

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSceneFixture(t *testing.T) {
	var view protocol.GraphView
	for i := 0; i < 15; i++ {
		view.Nodes = append(view.Nodes, protocol.Node{Name: fmt.Sprintf("project-%02d", i)})
	}
	view.Edges = []protocol.Edge{{From: "project-00", To: "project-01", Kind: "governs"}, {From: "*", To: "project-02", Kind: "depends"}}
	graph := New(view)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	graph.Message("project-00", "project-01", "request", now)
	s := BuildScene(State{Graph: graph, Camera: graph.Fit(80, 24), Now: now.Add(time.Second)}, Viewport{Width: 80, Height: 24})
	if len(s.Nodes) != 15 || len(s.Edges) != 1 || len(s.Comets) != 1 {
		t.Fatalf("scene has %d nodes, %d edges, %d comets", len(s.Nodes), len(s.Edges), len(s.Comets))
	}
	if !s.Nodes[0].Selected || !s.Nodes[2].Wildcard || s.Nodes[0].Label != "project-00" {
		t.Fatalf("unexpected nodes: %+v %+v", s.Nodes[0], s.Nodes[2])
	}
	if s.Edges[0].Kind != "governs" || !s.Edges[0].Dashed || s.Level != "labels" {
		t.Fatalf("unexpected scene: %+v", s)
	}
}
