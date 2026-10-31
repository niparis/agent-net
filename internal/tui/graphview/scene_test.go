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
	if len(s.Hints) == 0 || len(s.Hints[0].Keys) == 0 || s.Hints[0].Action != "zoom" {
		t.Fatalf("missing key hints: %+v", s.Hints)
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

func TestTalkSceneBucketsAndToggle(t *testing.T) {
	s, _ := Update(State{}, GraphChanged{Graph: protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: "depends"}}}})
	s, _ = Update(s, TalkLoaded{Counts: []protocol.GraphTalkCount{{From: "a", To: "b", Count: 1, HasEdge: true}, {From: "a", To: "c", Count: 5}, {From: "a", To: "d", Count: 20}}})
	scene := BuildScene(s, Viewport{Width: 80, Height: 20})
	if len(scene.Talk) != 3 || scene.Talk[0].Bucket != 1 || scene.Talk[0].NoRule || scene.Talk[1].Bucket != 2 || !scene.Talk[1].NoRule || scene.Talk[2].Bucket != 3 {
		t.Fatalf("talk: %+v", scene.Talk)
	}
	s, _ = Update(s, ToggleTalk{})
	if len(BuildScene(s, Viewport{Width: 80, Height: 20}).Talk) != 0 {
		t.Fatal("toggle did not hide talk")
	}
}

func TestActivityLevelsUseVisibleNodeQuantiles(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}, {Name: "f"}}})
	g.Positions = map[string]Point{"a": {-5, 0}, "b": {-3, 0}, "c": {-1, 0}, "d": {1, 0}, "e": {3, 0}, "f": {5, 0}}
	s := State{Graph: g, Camera: Camera{Zoom: 1, FitZoom: 1}, Talk: []protocol.GraphTalkCount{{From: "b", To: "x", Count: 1}, {From: "c", To: "x", Count: 2}, {From: "d", To: "x", Count: 3}, {From: "e", To: "x", Count: 4}, {From: "f", To: "x", Count: 5}}}
	scene := BuildScene(s, Viewport{80, 20})
	for i, n := range scene.Nodes {
		want := max(1, i)
		if n.Activity != want {
			t.Errorf("%s level %d want %d", n.Name, n.Activity, want)
		}
	}
}

func TestVoiceSceneAndRenderer(t *testing.T) {
	now := time.Unix(100, 0)
	g := New(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: "depends"}}})
	s, _ := Update(State{Graph: g}, TalkLoaded{Counts: []protocol.GraphTalkCount{{From: "a", To: "b", Count: 1, Voice: true}}})
	s, _ = Update(s, MessageEvent{Message: Message{From: "a", To: "b", Kind: "request", At: now, Voice: true}})
	s.Now = now.Add(time.Second)
	scene := BuildScene(s, Viewport{Width: 80, Height: 20})
	if len(scene.Talk) != 1 || !scene.Talk[0].Voice || len(scene.Comets) != 1 || !scene.Comets[0].Voice {
		t.Fatalf("voice scene: %+v %+v", scene.Talk, scene.Comets)
	}
	seenLine, seenPulse := false, false
	(Braille{Color: func(role, value string) string {
		if role == "voice" {
			if value == "•" {
				seenPulse = true
			} else {
				seenLine = true
			}
		}
		return value
	}}).Render(scene, Size{Width: 80, Height: 20})
	if !seenLine || !seenPulse {
		t.Fatalf("voice line %v pulse %v", seenLine, seenPulse)
	}
}
