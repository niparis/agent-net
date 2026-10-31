package graphview

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestUpdateActions(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	view := protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}}}
	s := State{Graph: New(view), Camera: Camera{Zoom: 1, FitZoom: 1}, Now: now}
	original := s.Graph.Selected()
	next, _ := Update(s, SelectNext{})
	if next.Graph.Selected() == original || s.Graph.Selected() != original {
		t.Fatal("selection must change without mutating input")
	}
	next, _ = Update(next, SelectPrev{})
	if next.Graph.Selected() != original {
		t.Fatal("previous selection")
	}
	next, _ = Update(s, ZoomIn{})
	if next.Camera.Zoom != 1.25 {
		t.Fatal("zoom in")
	}
	next, _ = Update(next, ZoomOut{})
	if next.Camera.Zoom != 1 {
		t.Fatal("zoom out")
	}
	next, _ = Update(s, Pan{DX: 4, DY: -2})
	if next.Camera.Center != (Point{4, -2}) {
		t.Fatal("pan")
	}
	fit := Camera{Zoom: 2, FitZoom: 2}
	next, _ = Update(s, Fit{Camera: fit})
	if next.Camera != fit {
		t.Fatal("fit")
	}
	next, _ = Update(s, ToggleOrbit{})
	if !next.Orbit {
		t.Fatal("orbit")
	}
	next, effect := Update(s, OpenSelected{})
	if effect.OpenMap != original {
		t.Fatal("open effect")
	}
	next, _ = Update(s, Tick{Now: now.Add(time.Second)})
	if !next.Now.Equal(now.Add(time.Second)) {
		t.Fatal("tick")
	}
	next, _ = Update(s, TogglePause{})
	if !next.Paused || !next.PausedAt.Equal(now) {
		t.Fatal("pause")
	}
	next, _ = Update(next, Tick{Now: now.Add(time.Second)})
	next, _ = Update(next, TogglePause{})
	if next.Paused {
		t.Fatal("resume")
	}
	next, _ = Update(s, MessageEvent{Message: Message{From: "a", To: "b", Kind: "request", At: now}})
	if len(next.Graph.Recent()) != 1 || len(s.Graph.Recent()) != 0 {
		t.Fatal("message must not mutate input")
	}
	next, _ = Update(s, GraphChanged{Graph: protocol.GraphView{Nodes: []protocol.Node{{Name: "c"}}}})
	if next.Graph.Selected() != "c" || s.Graph.Selected() != "a" {
		t.Fatal("graph change must not mutate input")
	}
}
