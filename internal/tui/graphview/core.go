// Package graphview contains the renderer-independent graph state.
package graphview

import (
	"math"
	"math/rand"
	"reflect"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

type Point struct{ X, Y float64 }
type Camera struct {
	Center         Point
	Zoom, FitZoom  float64
	ScaleX, ScaleY float64
}

func (c Camera) Project(p Point, width, height int) (float64, float64) {
	sx, sy := c.ScaleX, c.ScaleY
	if sx == 0 {
		sx = 1
	}
	if sy == 0 {
		sy = 1
	}
	return float64(width)/2 + (p.X-c.Center.X)*c.Zoom*2*sx, float64(height)/2 + (p.Y-c.Center.Y)*c.Zoom*sy
}
func (c Camera) Level() string {
	if c.Zoom < c.FitZoom*0.7 {
		return "clusters"
	}
	if c.Zoom > c.FitZoom*1.8 {
		return "cards"
	}
	return "labels"
}

// Renderer accepts a graph and viewport without coupling graph state to a UI toolkit.
type Renderer interface {
	Render(*Graph, Camera, int, int) []string
}

type Graph struct {
	Nodes     []protocol.Node
	Edges     []protocol.Edge
	Positions map[string]Point
	selected  int
	messages  []Message
}

func New(view protocol.GraphView) *Graph {
	g := NewWithoutLayout(view)
	g.layout()
	return g
}

func NewWithoutLayout(view protocol.GraphView) *Graph {
	g := &Graph{Nodes: append([]protocol.Node(nil), view.Nodes...), Edges: append([]protocol.Edge(nil), view.Edges...), Positions: map[string]Point{}}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].Name < g.Nodes[j].Name })
	sort.Slice(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Mode != b.Mode {
			return a.Mode < b.Mode
		}
		return a.Rule < b.Rule
	})
	return g
}
func (g *Graph) Selected() string {
	if len(g.Nodes) == 0 {
		return ""
	}
	return g.Nodes[g.selected].Name
}
func (g *Graph) Select(delta int) {
	if len(g.Nodes) > 0 {
		g.selected = ((g.selected+delta)%len(g.Nodes) + len(g.Nodes)) % len(g.Nodes)
	}
}
func (g *Graph) Fit(width, height int) Camera {
	if len(g.Positions) == 0 {
		return Camera{Zoom: 1, FitZoom: 1}
	}
	minx, miny, maxx, maxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range g.Positions {
		minx = math.Min(minx, p.X)
		maxx = math.Max(maxx, p.X)
		miny = math.Min(miny, p.Y)
		maxy = math.Max(maxy, p.Y)
	}
	zx := math.Max(1, float64(width)-4) / (2 * math.Max(0.001, maxx-minx))
	zy := math.Max(1, float64(height)-4) / math.Max(0.001, maxy-miny)
	if maxx == minx {
		zx = zy
	}
	if maxy == miny {
		zy = zx
	}
	z := math.Min(zx, zy)
	return Camera{Center: Point{(minx + maxx) / 2, (miny + maxy) / 2}, Zoom: z, FitZoom: z, ScaleX: zx / z, ScaleY: zy / z}
}
func (g *Graph) layout() {
	n := len(g.Nodes)
	if n == 0 {
		return
	}
	r := rand.New(rand.NewSource(11))
	p := make([]Point, n)
	index := map[string]int{}
	for i, node := range g.Nodes {
		index[node.Name] = i
		p[i] = Point{(r.Float64()*2 - 1), r.Float64()*2 - 1}
	}
	k := math.Sqrt(4/float64(n)) * 0.95
	temp := 0.35
	for it := 0; it < 600; it++ {
		d := make([]Point, n)
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx, dy := p[i].X-p[j].X, p[i].Y-p[j].Y
				dist := math.Max(math.Hypot(dx, dy), 0.001)
				f := k * k / (dist * dist)
				d[i].X += dx * f
				d[i].Y += dy * f
				d[j].X -= dx * f
				d[j].Y -= dy * f
			}
		}
		for _, e := range g.Edges {
			a, ok := index[e.From]
			b, ok2 := index[e.To]
			if !ok || !ok2 || a == b {
				continue
			}
			dx, dy := p[a].X-p[b].X, p[a].Y-p[b].Y
			dist := math.Hypot(dx, dy)
			f := dist / k
			d[a].X -= dx * f
			d[a].Y -= dy * f
			d[b].X += dx * f
			d[b].Y += dy * f
		}
		for i := range p {
			d[i].X -= p[i].X * 0.9 * k
			d[i].Y -= p[i].Y * 0.9 * k
			dist := math.Hypot(d[i].X, d[i].Y)
			if dist > 0 {
				f := math.Min(dist, temp) / dist
				p[i].X += d[i].X * f
				p[i].Y += d[i].Y * f
			}
		}
		temp = math.Max(0.004, temp*0.988)
	}
	for i, node := range g.Nodes {
		g.Positions[node.Name] = p[i]
	}
}

// Update recomputes positions only when the graph changes.
func (g *Graph) Update(view protocol.GraphView) bool {
	next := NewWithoutLayout(view)
	if reflect.DeepEqual(g.Nodes, next.Nodes) && reflect.DeepEqual(g.Edges, next.Edges) {
		return false
	}
	selected := g.Selected()
	g.Nodes, g.Edges = next.Nodes, next.Edges
	g.Positions = make(map[string]Point)
	g.layout()
	g.selected = 0
	for i, n := range g.Nodes {
		if n.Name == selected {
			g.selected = i
			break
		}
	}
	return true
}
