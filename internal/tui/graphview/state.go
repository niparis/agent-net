package graphview

import (
	"math"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// State holds graph interaction and animation state. Times are supplied by the caller.
type State struct {
	Graph                          *Graph
	neighbourAnchor, neighbourLast string
	Talk                           []protocol.GraphTalkCount
	Activity                       map[string]int
	TalkReady                      bool
	HideTalk                       bool
	Camera                         Camera
	Orbit, Paused, ReducedMotion   bool
	Now, PausedAt                  time.Time
}

// Action is a user intent or an external event.
type Action interface{ graphAction() }
type ZoomIn struct{}
type ZoomOut struct{}
type Pan struct{ DX, DY float64 }
type Fit struct{ Viewport Viewport }
type SelectNode struct{ Name string }
type SelectDirection struct {
	DX, DY   float64
	Viewport Viewport
}
type SelectNext struct{ Viewport Viewport }
type SelectPrev struct{ Viewport Viewport }
type ToggleOrbit struct{}
type ToggleTalk struct{}
type TogglePause struct{}
type OpenSelected struct{}
type Tick struct{ Now time.Time }
type GraphChanged struct{ Graph protocol.GraphView }
type TalkLoaded struct {
	Counts   []protocol.GraphTalkCount
	Activity map[string]int
}
type MessageEvent struct{ Message Message }
type FeedEvents struct {
	Events     []protocol.FeedEvent
	LastID     int64
	Now        time.Time
	Projects   map[string]string
	CountAfter int64
	CountsOnly bool
}

func (ZoomIn) graphAction()          {}
func (ZoomOut) graphAction()         {}
func (Pan) graphAction()             {}
func (Fit) graphAction()             {}
func (SelectNode) graphAction()      {}
func (SelectDirection) graphAction() {}
func (SelectNext) graphAction()      {}
func (SelectPrev) graphAction()      {}
func (ToggleTalk) graphAction()      {}
func (ToggleOrbit) graphAction()     {}
func (TogglePause) graphAction()     {}
func (OpenSelected) graphAction()    {}
func (Tick) graphAction()            {}
func (GraphChanged) graphAction()    {}
func (TalkLoaded) graphAction()      {}
func (MessageEvent) graphAction()    {}
func (FeedEvents) graphAction()      {}

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
	s.Talk = append([]protocol.GraphTalkCount(nil), s.Talk...)
	if s.Activity != nil {
		copied := make(map[string]int, len(s.Activity))
		for k, v := range s.Activity {
			copied[k] = v
		}
		s.Activity = copied
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
	case SelectNode:
		if s.Graph != nil {
			for i, n := range s.Graph.Nodes {
				if n.Name == a.Name {
					s.Graph.selected = i
					s.neighbourAnchor = ""
					s.neighbourLast = ""
					break
				}
			}
		}
	case SelectDirection:
		if s.Graph != nil {
			scene := BuildScene(s, a.Viewport)
			var origin Point
			found := false
			for _, n := range scene.Nodes {
				if n.Selected {
					origin = n.Position
					found = true
					break
				}
			}
			best, bestScore := -1, math.Inf(1)
			for i, n := range scene.Nodes {
				if n.Selected {
					continue
				}
				dx, dy := (n.Position.X-origin.X)*a.Viewport.Width, (n.Position.Y-origin.Y)*a.Viewport.Height
				forward := dx*a.DX + dy*a.DY
				if !found {
					dx = (n.Position.X - 0.5) * a.Viewport.Width
					dy = (n.Position.Y - 0.5) * a.Viewport.Height
					forward = 1
				}
				if forward <= 0 {
					continue
				}
				distance := math.Hypot(dx, dy)
				score := distance
				if found {
					score = distance * distance / forward
				}
				if score < bestScore {
					best, bestScore = i, score
				}
			}
			if best >= 0 {
				name := scene.Nodes[best].Name
				for i, n := range s.Graph.Nodes {
					if n.Name == name {
						s.Graph.selected = i
						break
					}
				}
			}
		}
	case SelectNext:
		s = selectNeighbour(s, a.Viewport, 1)
	case SelectPrev:
		s = selectNeighbour(s, a.Viewport, -1)
	case ToggleTalk:
		s.HideTalk = !s.HideTalk
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
		for _, pair := range s.Talk {
			if pair.From == protocol.GraphVoiceOwner || pair.To == protocol.GraphVoiceOwner {
				ensureVoiceOwner(s.Graph)
				break
			}
		}
	case TalkLoaded:
		s.Talk = append([]protocol.GraphTalkCount(nil), a.Counts...)
		s.Activity = a.Activity
		if a.Activity != nil {
			s.Activity = make(map[string]int, len(a.Activity))
			for k, v := range a.Activity {
				s.Activity[k] = v
			}
		}
		s.TalkReady = true
		for _, pair := range s.Talk {
			if pair.From == protocol.GraphVoiceOwner || pair.To == protocol.GraphVoiceOwner {
				ensureVoiceOwner(s.Graph)
				break
			}
		}
	case FeedEvents:
		{
			projects := make(map[string]string)
			if s.Graph != nil {
				for _, n := range s.Graph.Nodes {
					for _, session := range n.Sessions {
						projects[session.ID] = n.Name
					}
				}
			}
			for id, project := range a.Projects {
				projects[id] = project
			}
			for _, event := range a.Events {
				if event.ID <= a.LastID || event.Kind != "msg.stored" {
					continue
				}
				senderOwner := event.SenderOwner || event.FromCTO
				if s.TalkReady && event.ID > a.CountAfter && !event.Time.IsZero() && !event.Time.Before(a.Now.Add(-7*24*time.Hour)) && !event.Time.After(a.Now) {
					from, to := projects[event.From], projects[event.To]
					if s.Activity != nil {
						if from != "" && !senderOwner {
							s.Activity[from]++
						}
						if to != "" && !event.RecipientOwner && (to != from || senderOwner) {
							s.Activity[to]++
						}
					}
					if senderOwner {
						from = protocol.GraphVoiceOwner
						ensureVoiceOwner(s.Graph)
					}
					if event.RecipientOwner && (event.Voice || event.FromCTO) {
						to = protocol.GraphVoiceOwner
						ensureVoiceOwner(s.Graph)
					}
					if from != "" && to != "" && from != to && (event.Voice || senderOwner || (!senderOwner && !event.RecipientOwner)) {
						if from > to {
							from, to = to, from
						}
						i := 0
						for ; i < len(s.Talk); i++ {
							if s.Talk[i].From == from && s.Talk[i].To == to {
								break
							}
						}
						if i == len(s.Talk) {
							item := protocol.GraphTalkCount{From: from, To: to}
							if s.Graph != nil {
								for _, edge := range s.Graph.Edges {
									if edge.From == from && edge.To == to || edge.From == to && edge.To == from {
										item.HasEdge = true
										break
									}
								}
							}
							s.Talk = append(s.Talk, item)
						}
						if !senderOwner && !event.RecipientOwner {
							s.Talk[i].Count++
						}
						s.Talk[i].Voice = s.Talk[i].Voice || event.Voice || senderOwner
					}
				}
				if a.CountsOnly || s.Graph == nil {
					continue
				}
				at := event.Time
				if at.IsZero() || at.Before(a.Now.Add(-travelTime)) {
					at = a.Now
				}
				from, to := projects[event.From], projects[event.To]
				voice := event.Voice || senderOwner
				if senderOwner {
					from = protocol.GraphVoiceOwner
					ensureVoiceOwner(s.Graph)
				}
				if event.RecipientOwner && voice {
					to = protocol.GraphVoiceOwner
					ensureVoiceOwner(s.Graph)
				}
				s.Graph.Message(from, to, event.Type, at, voice, senderOwner)
			}
		}
	case MessageEvent:
		if s.Graph != nil {
			m := a.Message
			s.Graph.Message(m.From, m.To, m.Kind, m.At, m.Voice)
		}
	}
	return s, effect
}

// selectNeighbour keeps an anchor while stepping through its angular neighbours.
func selectNeighbour(s State, viewport Viewport, direction int) State {
	if s.Graph == nil {
		return s
	}
	if viewport.Width <= 0 || viewport.Height <= 0 {
		viewport = Viewport{80, 20}
	}
	scene := BuildScene(s, viewport)
	current := s.Graph.Selected()
	if current == "" {
		best, dist := "", math.Inf(1)
		for _, n := range scene.Nodes {
			d := math.Hypot((n.Position.X-.5)*viewport.Width, (n.Position.Y-.5)*viewport.Height)
			if d < dist {
				best, dist = n.Name, d
			}
		}
		for i, n := range s.Graph.Nodes {
			if n.Name == best {
				s.Graph.selected = i
				break
			}
		}
		s.neighbourAnchor, s.neighbourLast = "", ""
		return s
	}
	if current != s.neighbourLast {
		s.neighbourAnchor = current
	}
	anchor := s.neighbourAnchor
	linked := map[string]bool{}
	for _, e := range s.Graph.Edges {
		if e.From == anchor {
			linked[e.To] = true
		}
		if e.To == anchor {
			linked[e.From] = true
		}
	}
	for _, e := range s.Talk {
		if e.Count <= 0 {
			continue
		}
		if e.From == anchor {
			linked[e.To] = true
		}
		if e.To == anchor {
			linked[e.From] = true
		}
	}
	var origin Point
	for _, n := range scene.Nodes {
		if n.Name == anchor {
			origin = n.Position
			break
		}
	}
	type neighbour struct {
		name  string
		angle float64
	}
	var ns []neighbour
	for _, n := range scene.Nodes {
		if linked[n.Name] && n.Name != anchor {
			angle := math.Atan2((n.Position.Y-origin.Y)*viewport.Height, (n.Position.X-origin.X)*viewport.Width)
			if angle < 0 {
				angle += 2 * math.Pi
			}
			ns = append(ns, neighbour{n.Name, angle})
		}
	}
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].angle == ns[j].angle {
			return ns[i].name < ns[j].name
		}
		return ns[i].angle < ns[j].angle
	})
	if len(ns) == 0 {
		return s
	}
	idx := -1
	for i, n := range ns {
		if n.name == current {
			idx = i
			break
		}
	}
	if idx < 0 {
		if direction > 0 {
			idx = 0
		} else {
			idx = len(ns) - 1
		}
	} else {
		idx = (idx + direction + len(ns)) % len(ns)
	}
	for i, n := range s.Graph.Nodes {
		if n.Name == ns[idx].name {
			s.Graph.selected = i
			s.neighbourLast = n.Name
			break
		}
	}
	return s
}

// The owner is a graph origin, not a project or a selectable graph node.
func ensureVoiceOwner(g *Graph) {
	if g == nil {
		return
	}
	if _, ok := g.Positions[protocol.GraphVoiceOwner]; ok {
		return
	}
	if len(g.Positions) == 0 {
		return
	}
	minX, minY := math.Inf(1), math.Inf(1)
	for _, p := range g.Positions {
		minX = math.Min(minX, p.X)
		minY = math.Min(minY, p.Y)
	}
	g.Positions[protocol.GraphVoiceOwner] = Point{X: minX, Y: minY}
}
