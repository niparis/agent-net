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
	fit := s.Graph.Fit(80, 20)
	next, _ = Update(s, Fit{Viewport: Viewport{Width: 80, Height: 20}})
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

func TestFitAndFeedEventsUseState(t *testing.T) {
	now := time.Unix(100, 0)
	view := protocol.GraphView{Nodes: []protocol.Node{{Name: "alpha", Sessions: []protocol.GraphSession{{ID: "s1"}}}, {Name: "beta", Sessions: []protocol.GraphSession{{ID: "s2"}}}}}
	s := State{Graph: New(view)}
	next, _ := Update(s, Fit{Viewport: Viewport{Width: 80, Height: 20}})
	if next.Camera != s.Graph.Fit(80, 20) {
		t.Fatal("fit must derive camera from graph and viewport")
	}
	events := []protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", Time: now.Add(-time.Minute)}, From: "s1", To: "s2", Type: "request"}, {Event: protocol.Event{ID: 2, Kind: "other", Time: now}, From: "s1", To: "s2"}}
	next, _ = Update(next, FeedEvents{Events: events, Now: now})
	comets, _ := next.Graph.Traffic(now.Add(time.Second))
	if len(comets) != 1 || comets[0].From != "alpha" || comets[0].To != "beta" || !comets[0].At.Equal(now) {
		t.Fatalf("events: %+v", comets)
	}
	if len(s.Graph.Recent()) != 0 {
		t.Fatal("mutated input")
	}
	next, _ = Update(next, FeedEvents{Events: events, LastID: 1, Now: now})
	if len(next.Graph.Recent()) != 1 {
		t.Fatal("replayed old event")
	}
}

func TestPausePreservesTrafficProgress(t *testing.T) {
	now := time.Unix(100, 0)
	s := State{Graph: New(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}}}), Now: now}
	s, _ = Update(s, MessageEvent{Message: Message{From: "a", To: "b", At: now}})
	s, _ = Update(s, Tick{Now: now.Add(time.Second)})
	s, _ = Update(s, TogglePause{})
	s, _ = Update(s, Tick{Now: now.Add(10 * time.Second)})
	scene := BuildScene(s, Viewport{Width: 80, Height: 20})
	if len(scene.Comets) != 1 {
		t.Fatalf("traffic did not remain paused: %+v", scene.Comets)
	}
	paused := scene.Comets[0].Position
	s, _ = Update(s, TogglePause{})
	scene = BuildScene(s, Viewport{Width: 80, Height: 20})
	if len(scene.Comets) != 1 || scene.Comets[0].Position != paused {
		t.Fatalf("traffic jumped on resume: %+v", scene.Comets)
	}
}

func TestTalkCountsAndLiveEvents(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s, _ := Update(State{}, GraphChanged{Graph: protocol.GraphView{Nodes: []protocol.Node{{Name: "alpha", Sessions: []protocol.GraphSession{{ID: "a"}}}, {Name: "beta", Sessions: []protocol.GraphSession{{ID: "b"}}}}, Edges: []protocol.Edge{{From: "alpha", To: "beta", Kind: "depends"}}}})
	s, _ = Update(s, TalkLoaded{Counts: []protocol.GraphTalkCount{{From: "alpha", To: "beta", Count: 2, HasEdge: true}}})
	s, _ = Update(s, FeedEvents{Now: now, Events: []protocol.FeedEvent{{Event: protocol.Event{ID: 1, Kind: "msg.stored", Time: now}, From: "b", To: "a"}}})
	if len(s.Talk) != 1 || s.Talk[0].Count != 3 || !s.Talk[0].HasEdge {
		t.Fatalf("talk: %+v", s.Talk)
	}
}
