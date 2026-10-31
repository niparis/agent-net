package graphview

import (
	"math"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// State holds graph interaction and animation state. Times are supplied by the caller.
type State struct {
	Graph                        *Graph
	Camera                       Camera
	Orbit, Paused, ReducedMotion bool
	Now, PausedAt                time.Time
}

// Action is a user intent or an external event.
type Action interface{ graphAction() }
type ZoomIn struct{}
type ZoomOut struct{}
type Pan struct{ DX, DY float64 }
type Fit struct{ Viewport Viewport }
type SelectNext struct{}
type SelectPrev struct{}
type ToggleOrbit struct{}
type TogglePause struct{}
type OpenSelected struct{}
type Tick struct{ Now time.Time }
type GraphChanged struct{ Graph protocol.GraphView }
type MessageEvent struct{ Message Message }
type FeedEvents struct {
	Events []protocol.FeedEvent
	LastID int64
	Now    time.Time
}

func (ZoomIn) graphAction()       {}
func (ZoomOut) graphAction()      {}
func (Pan) graphAction()          {}
func (Fit) graphAction()          {}
func (SelectNext) graphAction()   {}
func (SelectPrev) graphAction()   {}
func (ToggleOrbit) graphAction()  {}
func (TogglePause) graphAction()  {}
func (OpenSelected) graphAction() {}
func (Tick) graphAction()         {}
func (GraphChanged) graphAction() {}
func (MessageEvent) graphAction() {}
func (FeedEvents) graphAction()   {}

// Effect describes work for the host UI to perform.
type Effect struct {
	OpenMap string
	Open    bool
}

// Update returns a new state without changing the input state or its graph.
func Update(s State, action Action) (State, Effect) {
	if s.Graph != nil {
		graph := *s.Graph
		graph.Nodes = append([]protocol.Node(nil), graph.Nodes...)
		graph.Edges = append([]protocol.Edge(nil), graph.Edges...)
		graph.messages = append([]Message(nil), graph.messages...)
		graph.Positions = make(map[string]Point, len(s.Graph.Positions))
		for name, p := range s.Graph.Positions {
			graph.Positions[name] = p
		}
		s.Graph = &graph
	}
	var effect Effect
	switch a := action.(type) {
	case ZoomIn:
		s.Camera.Zoom = math.Min(s.Camera.Zoom*1.25, s.Camera.FitZoom*8)
	case ZoomOut:
		s.Camera.Zoom = math.Max(s.Camera.Zoom/1.25, s.Camera.FitZoom*0.3)
	case Pan:
		if s.Camera.Zoom != 0 {
			s.Camera.Center.X += a.DX / s.Camera.Zoom
			s.Camera.Center.Y += a.DY / s.Camera.Zoom
		}
	case Fit:
		if s.Graph != nil {
			s.Camera = s.Graph.Fit(int(a.Viewport.Width), int(a.Viewport.Height))
		}
	case SelectNext:
		if s.Graph != nil {
			s.Graph.Select(1)
		}
	case SelectPrev:
		if s.Graph != nil {
			s.Graph.Select(-1)
		}
	case ToggleOrbit:
		s.Orbit = !s.Orbit
	case TogglePause:
		if s.Paused {
			if s.Graph != nil {
				s.Graph.ShiftTraffic(s.PausedAt, s.Now)
			}
			s.Paused = false
		} else {
			s.Paused = true
			s.PausedAt = s.Now
		}
	case OpenSelected:
		if s.Graph != nil {
			effect.OpenMap = s.Graph.Selected()
			effect.Open = true
		}
	case Tick:
		s.Now = a.Now
	case GraphChanged:
		if s.Graph == nil {
			s.Graph = New(a.Graph)
		} else {
			s.Graph.Update(a.Graph)
		}
	case FeedEvents:
		if s.Graph != nil {
			projects := make(map[string]string)
			for _, n := range s.Graph.Nodes {
				for _, session := range n.Sessions {
					projects[session.ID] = n.Name
				}
			}
			for _, event := range a.Events {
				if event.ID <= a.LastID || event.Kind != "msg.stored" {
					continue
				}
				at := event.Time
				if at.IsZero() || at.Before(a.Now.Add(-travelTime)) {
					at = a.Now
				}
				s.Graph.Message(projects[event.From], projects[event.To], event.Type, at)
			}
		}
	case MessageEvent:
		if s.Graph != nil {
			m := a.Message
			s.Graph.Message(m.From, m.To, m.Kind, m.At)
		}
	}
	return s, effect
}
