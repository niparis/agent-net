package graphview

import (
	"github.com/niparis/agent-net/internal/protocol"
	"math"
	"sort"
)

// Viewport describes the shape of a view in abstract units.
type Viewport struct{ Width, Height float64 }
type SceneNode struct {
	Activity                                  int
	Name, Label, Role, PreferredSide          string
	Position                                  Point
	Selected, Flashing, Wildcard              bool
	Architect, HasSessions, Pulse, HasCharter bool
	Card                                      SceneCard
}
type SceneCard struct {
	Name, Maturity, Purpose      string
	Sessions, Incoming, Outgoing int
}
type SceneEdge struct {
	From, To, Arrow Point
	Kind            string
	Dashed          bool
}
type SceneTalk struct {
	From, To Point
	Bucket   int
	NoRule   bool
}
type SceneComet struct {
	Position Point
	Trail    []Point
}

type KeyHint struct {
	Keys   []string
	Action string
}

// Scene contains semantic elements in normalized viewport coordinates.
type Scene struct {
	Nodes  []SceneNode
	Edges  []SceneEdge
	Talk   []SceneTalk
	Comets []SceneComet
	Level  string
	Feed   []Message
	Paused bool
	Hints  []KeyHint
}

// BuildScene derives a renderer-independent view without changing state.
func BuildScene(s State, viewport Viewport) Scene {
	scene := Scene{Level: s.Camera.Level(), Paused: s.Paused, Hints: []KeyHint{{Keys: []string{"+", "-"}, Action: "zoom"}, {Keys: []string{"h", "j", "k", "l"}, Action: "pan"}, {Keys: []string{"0"}, Action: "fit"}, {Keys: []string{"[", "]"}, Action: "select"}, {Keys: []string{"o"}, Action: "orbit"}, {Keys: []string{"t"}, Action: "talk"}, {Keys: []string{"enter"}, Action: "open"}, {Keys: []string{"space"}, Action: "pause"}, {Keys: []string{"?"}, Action: "help"}}}
	if s.Graph == nil || viewport.Width <= 0 || viewport.Height <= 0 {
		return scene
	}
	g := *s.Graph
	if s.Orbit {
		g.Positions = orbitPositions(&g)
	}
	cam := s.Camera
	if cam.Zoom <= 0 {
		cam = g.Fit(int(viewport.Width), int(viewport.Height))
		scene.Level = cam.Level()
	}
	project := func(p Point) Point {
		sx, sy := cam.ScaleX, cam.ScaleY
		if sx == 0 {
			sx = 1
		}
		if sy == 0 {
			sy = 1
		}
		return Point{0.5 + (p.X-cam.Center.X)*cam.Zoom*2*sx/viewport.Width, 0.5 + (p.Y-cam.Center.Y)*cam.Zoom*sy/viewport.Height}
	}
	now := s.Now
	if s.Paused {
		now = s.PausedAt
	}
	comets, flashes := g.Traffic(now)
	flash := map[string]bool{}
	for _, name := range flashes {
		flash[name] = true
	}
	for _, n := range g.Nodes {
		role := "none"
		if n.Kind == "architect" {
			role = "architect"
		} else if n.SessionCount > 0 || len(n.Sessions) > 0 {
			role = "idle"
		}
		for _, session := range n.Sessions {
			if session.Status == "busy" {
				role = "busy"
				break
			}
		}
		node := SceneNode{Name: n.Name, Label: n.Name, Role: role, PreferredSide: "right", Position: project(g.Positions[n.Name]), Selected: n.Name == g.Selected(), Flashing: flash[n.Name], HasCharter: n.Charter != (protocol.Charter{}), Architect: n.Kind == "architect", HasSessions: n.SessionCount > 0 || len(n.Sessions) > 0, Pulse: role == "busy" && !s.ReducedMotion && now.UnixMilli()%1000 >= 500, Card: SceneCard{Name: n.Name, Maturity: n.Charter.Maturity, Purpose: n.Charter.Purpose, Sessions: max(n.SessionCount, len(n.Sessions))}}
		for _, e := range g.Edges {
			if e.From == n.Name {
				node.Card.Outgoing++
			}
			if e.To == n.Name {
				node.Card.Incoming++
			}
		}
		for _, e := range g.Edges {
			if e.From == "*" && e.To == n.Name {
				node.Wildcard = true
				break
			}
		}
		scene.Nodes = append(scene.Nodes, node)
	}
	counts := map[string]int{}
	for _, t := range s.Talk {
		if t.Count > 0 {
			counts[t.From] += t.Count
			counts[t.To] += t.Count
		}
	}
	var values []int
	for _, n := range scene.Nodes {
		if n.Position.X >= 0 && n.Position.X < 1 && n.Position.Y >= 0 && n.Position.Y < 1 && counts[n.Name] > 0 {
			values = append(values, counts[n.Name])
		}
	}
	sort.Ints(values)
	for i := range scene.Nodes {
		count := counts[scene.Nodes[i].Name]
		scene.Nodes[i].Activity = 1
		if count > 0 && len(values) > 1 {
			rank := sort.SearchInts(values, count)
			scene.Nodes[i].Activity = 1 + rank*4/(len(values)-1)
		}
	}
	if !s.HideTalk {
		for _, talk := range s.Talk {
			a, ok := g.Positions[talk.From]
			b, ok2 := g.Positions[talk.To]
			if !ok || !ok2 || talk.Count <= 0 {
				continue
			}
			bucket := 1
			if talk.Count >= 20 {
				bucket = 3
			} else if talk.Count >= 5 {
				bucket = 2
			}
			hasEdge := false
			for _, e := range g.Edges {
				if e.From == talk.From && e.To == talk.To || e.From == talk.To && e.To == talk.From {
					hasEdge = true
					break
				}
			}
			scene.Talk = append(scene.Talk, SceneTalk{From: project(a), To: project(b), Bucket: bucket, NoRule: !hasEdge})
		}
	}
	for _, e := range g.Edges {
		a, ok := g.Positions[e.From]
		b, ok2 := g.Positions[e.To]
		if !ok || !ok2 {
			continue
		}
		a, b = project(a), project(b)
		scene.Edges = append(scene.Edges, SceneEdge{From: a, To: b, Arrow: Point{a.X*0.2 + b.X*0.8, a.Y*0.2 + b.Y*0.8}, Kind: e.Kind, Dashed: e.Kind == "governs"})
	}
	for _, c := range comets {
		comet := SceneComet{Position: project(c.Position)}
		a, b := g.Positions[c.From], g.Positions[c.To]
		for i := 3; i >= 1; i-- {
			u := c.Progress - float64(i)*0.035
			if u >= 0 {
				comet.Trail = append(comet.Trail, project(Point{a.X + (b.X-a.X)*u, a.Y + (b.Y-a.Y)*u}))
			}
		}
		scene.Comets = append(scene.Comets, comet)
	}
	scene.Feed = g.Recent()
	return scene
}

func orbitPositions(g *Graph) map[string]Point {
	sel := g.Selected()
	dist := map[string]int{sel: 0}
	q := []string{sel}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		for _, e := range g.Edges {
			other := ""
			if e.From == n {
				other = e.To
			} else if e.To == n {
				other = e.From
			}
			if other == "" || other == "*" {
				continue
			}
			if _, ok := g.Positions[other]; !ok {
				continue
			}
			if _, ok := dist[other]; !ok {
				dist[other] = dist[n] + 1
				q = append(q, other)
			}
		}
	}
	rings := map[int][]string{}
	for _, n := range g.Nodes {
		d, ok := dist[n.Name]
		if !ok || d > 3 {
			d = 3
		}
		rings[d] = append(rings[d], n.Name)
	}
	center := g.Positions[sel]
	out := map[string]Point{}
	for d, names := range rings {
		for i, name := range names {
			a := 2*math.Pi*float64(i)/float64(len(names)) - math.Pi/2
			radius := float64(d) * 0.65
			out[name] = Point{center.X + radius*math.Cos(a), center.Y + radius*math.Sin(a)}
		}
	}
	return out
}
