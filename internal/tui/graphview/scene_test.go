package graphview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSceneFixture(t *testing.T) {
	var view protocol.GraphView
	for i := 0; i < 15; i++ {
		node := protocol.Node{Name: fmt.Sprintf("project-%02d", i), Kind: "component"}
		if i == 0 {
			node.Name = "architect"
			node.Kind = "architect"
			node.Charter = protocol.Charter{Maturity: "growing", Purpose: "coordinates work"}
		}
		if i < 6 {
			node.SessionCount = 1
			node.Sessions = []protocol.GraphSession{{ID: fmt.Sprintf("session-%d", i), Status: "idle"}}
		}
		if i == 1 {
			node.Sessions[0].Status = "busy"
		}
		view.Nodes = append(view.Nodes, node)
	}
	view.Edges = []protocol.Edge{{From: "architect", To: "project-01", Kind: "governs"}, {From: "project-01", To: "project-02", Kind: "depends"}, {From: "project-02", To: "project-03", Kind: "deploys"}, {From: "*", To: "project-03", Kind: "depends"}, {From: "project-03", To: "project-04", Kind: "depends"}, {From: "project-04", To: "project-05", Kind: "depends"}}
	graph := New(view)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	graph.Message("project-01", "project-02", "request", now)
	graph.Message("project-04", "project-05", "reply", now.Add(-time.Second))
	s := BuildScene(State{Graph: graph, Camera: graph.Fit(80, 24), Now: now.Add(time.Second)}, Viewport{Width: 80, Height: 24})
	if len(s.Nodes) != 15 || len(s.Edges) != 5 || len(s.Comets) != 2 {
		t.Fatalf("scene has %d nodes, %d edges, %d comets", len(s.Nodes), len(s.Edges), len(s.Comets))
	}
	if !s.Nodes[0].Selected || !s.Nodes[3].Wildcard || s.Nodes[0].Label != "architect" {
		t.Fatalf("unexpected nodes: %+v %+v", s.Nodes[0], s.Nodes[2])
	}
	if s.Nodes[0].Card.Maturity != "growing" || s.Nodes[0].Card.Purpose != "coordinates work" || s.Nodes[0].Card.Sessions != 1 || s.Nodes[0].Card.Outgoing != 1 || s.Nodes[3].Card.Incoming != 2 {
		t.Fatalf("missing card details: %+v %+v", s.Nodes[0].Card, s.Nodes[3].Card)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join("testdata", "scene_15.json")
	if os.Getenv("UPDATE_SCENE_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Fatal("scene differs from snapshot")
	}
	if s.Edges[0].Kind != "governs" || !s.Edges[0].Dashed || s.Level != "labels" {
		t.Fatalf("unexpected scene: %+v", s)
	}
}
