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
	BusySessions                              int
	WildcardLabel                             string
	Name, Label, Kind, Role, PreferredSide    string
	Position                                  Point
	Selected, Flashing, Wildcard              bool
	Architect, HasSessions, Pulse, HasCharter bool
	// ShowCard is set when pixel mode is zoomed in far enough to draw the
	// node's charter card; PulsePhase is the position in the slow busy pulse.
	ShowCard   bool
	PulsePhase float64
	Card       SceneCard
}
type SceneCard struct {
	Name, Maturity, Purpose      string
	Sessions, Incoming, Outgoing int
}
type SceneEdge struct {
	FromName, ToName string
	From, To, Arrow  Point
	Kind, Mode, Rule string
	Dashed           bool
	Selected         bool
	Dimmed           bool
}
type SceneTalk struct {
	From, To Point
	Bucket   int
	NoRule   bool
	Voice    bool
}
type SceneComet struct {
	Position Point
	Trail    []Point
	Voice    bool
	Kind     string
	From, To Point
	Progress float64
	HasEdge  bool
}

// SceneRipple is the ring that marks a message arriving at its target. The
// Braille renderer ignores it; the pixel renderer draws it.
type SceneRipple struct {
	Position Point
	Kind     string
	Voice    bool
	Progress float64
}

type KeyHint struct {
	Keys   []string
	Action string
}

// Scene contains semantic elements in normalized viewport coordinates.
type Scene struct {
	Groups     []Group
	Nodes      []SceneNode
	Edges      []SceneEdge
	Talk       []SceneTalk
	Comets     []SceneComet
	Ripples    []SceneRipple
	Level      string
	Feed       []Message
	Paused     bool
	Hints      []KeyHint
	ZoomRatio  float64
	GridOrigin Point
}

// BuildScene derives a renderer-independent view without changing state.
func BuildScene(s State, viewport Viewport) Scene {
	scene := Scene{Level: s.Camera.Level(), Paused: s.Paused, Hints: []KeyHint{{Keys: []string{"+", "-"}, Action: "zoom"}, {Keys: []string{"h", "j", "k", "l"}, Action: "pan"}, {Keys: []string{"0"}, Action: "fit"}, {Keys: []string{"[", "]"}, Action: "select"}, {Keys: []string{"o"}, Action: "orbit"}, {Keys: []string{"t"}, Action: "talk"}, {Keys: []string{"enter"}, Action: "open"}, {Keys: []string{"space"}, Action: "pause"}, {Keys: []string{"?"}, Action: "help"}}}
	if s.Graph == nil || viewport.Width <= 0 || viewport.Height <= 0 {
		return scene
	}
	g := *s.Graph
	_, ownerVisible := g.Positions[protocol.GraphVoiceOwner]
	if s.Orbit {
		g.Positions = orbitPositions(&g)
	} else {
		positions := make(map[string]Point, len(g.Positions))
		for name, p := range g.Positions {
			positions[name] = p
		}
		g.Positions = positions
	}
	cam := s.ViewCamera()
	if cam.Zoom <= 0 {
		cam = g.Fit(int(viewport.Width), int(viewport.Height))
	}
	scene.Level = cam.Level()
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
	// Screen-pinned markers have world positions only to share projection with
	// edges and traffic. They do not participate in layout or fitting.
	sx, sy := cam.ScaleX, cam.ScaleY
	if sx == 0 {
		sx = 1
	}
	if sy == 0 {
		sy = 1
	}
	pin := func(y float64) Point {
		return Point{cam.Center.X + (2.5-viewport.Width/2)/(cam.Zoom*2*sx), cam.Center.Y + (y-viewport.Height/2)/(cam.Zoom*sy)}
	}
	if ownerVisible {
		g.Positions[protocol.GraphVoiceOwner] = pin(2)
	}
	if _, ok := g.Positions[protocol.CTONodeName]; ok {
		g.Positions[protocol.CTONodeName] = pin(4)
	}
	scene.ZoomRatio = 1
	if cam.FitZoom > 0 {
		scene.ZoomRatio = cam.Zoom / cam.FitZoom
	}
	scene.GridOrigin = project(Point{})
	now := s.Now
	if s.Paused {
		now = s.PausedAt
	}
	showCards := cam.FitZoom > 0 && cam.Zoom >= cam.FitZoom*1.9
	comets, flashes := g.Traffic(now)
	flash := map[string]bool{}
	for _, name := range flashes {
		flash[name] = true
	}
	selected := g.Selected()
	for _, n := range g.Nodes {
		role := "none"
		if n.Kind == "architect" {
			role = "architect"
		} else if n.Kind == "cto" {
			role = "cto"
		} else if n.SessionCount > 0 || len(n.Sessions) > 0 {
			role = "idle"
		}
		for _, session := range n.Sessions {
			if session.Status == "busy" {
				role = "busy"
			}
		}
		node := SceneNode{Name: n.Name, Label: n.Name, Kind: n.Kind, Role: role, PreferredSide: "right", Position: project(g.Positions[n.Name]), Selected: n.Name == selected, Flashing: flash[n.Name], HasCharter: n.Charter != (protocol.Charter{}), Architect: n.Kind == "architect", HasSessions: n.SessionCount > 0 || len(n.Sessions) > 0, Pulse: role == "busy" && !s.ReducedMotion && now.UnixMilli()%1000 >= 500, ShowCard: showCards && n.Charter != (protocol.Charter{}), Card: SceneCard{Name: n.Name, Maturity: n.Charter.Maturity, Purpose: n.Charter.Purpose, Sessions: max(n.SessionCount, len(n.Sessions))}}
		for _, session := range n.Sessions {
			if session.Status == "busy" {
				node.BusySessions++
			}
		}
		if role == "busy" {
			if s.ReducedMotion {
				node.PulsePhase = .5
			} else {
				node.PulsePhase = .5 + .5*math.Sin(float64(now.UnixMilli()%2200)/2200*2*math.Pi)
			}
		}
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
				node.WildcardLabel = "all projects " + e.Kind
				if e.Kind == "depends" {
					node.WildcardLabel = "all projects depend"
				}
				if e.Mode != "" {
					node.WildcardLabel += " · " + e.Mode
				}
				if e.Rule != "" {
					node.WildcardLabel += " · " + e.Rule
				}
				break
			}
		}
		scene.Nodes = append(scene.Nodes, node)
	}
	if p, ok := g.Positions[protocol.GraphVoiceOwner]; ok {
		scene.Nodes = append(scene.Nodes, SceneNode{Name: protocol.GraphVoiceOwner, Label: "you", Kind: "owner", Role: "voice", Position: project(p), Activity: 1, Selected: selected == protocol.GraphVoiceOwner})
	}
	counts := map[string]int{}
	for _, t := range s.Talk {
		if t.Count > 0 {
			counts[t.From] += t.Count
			counts[t.To] += t.Count
		}
	}
	if s.Activity != nil {
		counts = s.Activity
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
			if !ok || !ok2 || talk.Count <= 0 && !talk.Voice || !s.ShowAllTalk && (selected == "" || talk.From != selected && talk.To != selected) {
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
			scene.Talk = append(scene.Talk, SceneTalk{From: project(a), To: project(b), Bucket: bucket, NoRule: !hasEdge, Voice: talk.Voice})
		}
	}
	for _, e := range g.Edges {
		a, ok := g.Positions[e.From]
		b, ok2 := g.Positions[e.To]
		if !ok || !ok2 {
			continue
		}
		a, b = project(a), project(b)
		accented := selected != "" && (e.From == selected || e.To == selected)
		scene.Edges = append(scene.Edges, SceneEdge{FromName: e.From, ToName: e.To, From: a, To: b, Arrow: Point{a.X*0.2 + b.X*0.8, a.Y*0.2 + b.Y*0.8}, Kind: e.Kind, Mode: e.Mode, Rule: e.Rule, Dashed: e.Kind == "governs", Selected: accented, Dimmed: selected != "" && !accented})
	}
	for _, c := range comets {
		a, b := g.Positions[c.From], g.Positions[c.To]
		hasEdge := false
		for _, e := range g.Edges {
			if e.From == c.From && e.To == c.To || e.From == c.To && e.To == c.From {
				hasEdge = true
				break
			}
		}
		comet := SceneComet{Position: project(c.Position), Voice: c.Voice, Kind: c.Kind, From: project(a), To: project(b), Progress: c.Progress, HasEdge: hasEdge}
		for i := 3; i >= 1; i-- {
			u := c.Progress - float64(i)*0.035
			if u >= 0 {
				comet.Trail = append(comet.Trail, project(Point{a.X + (b.X-a.X)*u, a.Y + (b.Y-a.Y)*u}))
			}
		}
		scene.Comets = append(scene.Comets, comet)
	}
	for _, m := range g.messages {
		age := now.Sub(m.At)
		if age < travelTime || age >= travelTime+flashTime {
			continue
		}
		if _, ok := g.Positions[m.From]; !ok {
			continue
		}
		b, ok := g.Positions[m.To]
		if !ok {
			continue
		}
		scene.Ripples = append(scene.Ripples, SceneRipple{Position: project(b), Kind: m.Kind, Voice: m.Voice, Progress: float64(age-travelTime) / float64(flashTime)})
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
