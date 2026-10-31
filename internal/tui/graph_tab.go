package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

type graphTickMsg struct{ generation int }

func graphTick(generation int) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return graphTickMsg{generation} })
}

func (m Model) graphSize() (int, int) {
	w, h := max(m.width, 40), max(m.height, 10)
	box := min(max((h-1)/3, 6), max(h-4, 1))
	return max(w-4, 1), max(h-1-box-4, 1)
}
func (m Model) graphKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.graphHelp {
		if k.String() == "?" || k.String() == "esc" {
			m.graphHelp = false
		}
		return m, nil
	}
	if k.String() == "q" {
		m.quitting = true
		return m, tea.Quit
	}
	if m.graph == nil {
		if k.String() == "?" {
			m.graphHelp = true
		}
		return m, nil
	}
	switch k.String() {
	case "+", "=":
		m.graphCamera.Zoom = math.Min(m.graphCamera.Zoom*1.25, m.graphCamera.FitZoom*8)
	case "-":
		m.graphCamera.Zoom = math.Max(m.graphCamera.Zoom/1.25, m.graphCamera.FitZoom*0.3)
	case "left", "h":
		m.graphCamera.Center.X -= 4 / m.graphCamera.Zoom
	case "right", "l":
		m.graphCamera.Center.X += 4 / m.graphCamera.Zoom
	case "up", "k":
		m.graphCamera.Center.Y -= 2 / m.graphCamera.Zoom
	case "down", "j":
		m.graphCamera.Center.Y += 2 / m.graphCamera.Zoom
	case "0":
		w, h := m.graphSize()
		m.graphCamera = m.graph.Fit(w, h)
	case "[":
		m.graph.Select(-1)
	case "]":
		m.graph.Select(1)
	case "o":
		m.graphOrbit = !m.graphOrbit
	case " ":
		if m.graphPaused {
			m.graph.ShiftTraffic(m.graphPausedAt, m.now())
			m.graphPaused = false
		} else {
			m.graphPaused = true
			m.graphPausedAt = m.now()
		}
	case "?":
		m.graphHelp = true
	case "enter":
		m.mp.selected = m.graph.Selected()
		m.mp.overview = false
		return m.startMap()
	}
	return m, nil
}
func (m Model) graphView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if m.graphHelp {
		return append(out, sDim.Render("  + - zoom  arrows/hjkl pan  0 fit  [ ] select  o orbit"), sDim.Render("  enter Map  space pause  ? close  tab CTO"))
	}
	if m.graph == nil {
		return append(out, sDim.Render("  reading the graph..."))
	}
	gw, gh := max(w-4, 1), max(h-4, 1)
	cam := m.graphCamera
	if cam.Zoom <= 0 {
		cam = m.graph.Fit(gw, gh)
	}
	g := m.graph
	if m.graphOrbit {
		copyGraph := *g
		copyGraph.Positions = graphOrbitPositions(g)
		g = &copyGraph
	}
	now := m.now()
	if m.graphPaused {
		now = m.graphPausedAt
	}
	scene := graphview.BuildScene(graphview.State{Graph: g, Camera: cam, Now: now}, graphview.Viewport{Width: float64(gw), Height: float64(gh)})
	renderer := graphview.Braille{Styled: true}
	for _, line := range renderer.Render(scene, graphview.Size{Width: gw, Height: gh}).Lines {
		out = append(out, "  "+line)
	}
	feed := []string{}
	for _, msg := range m.graph.Recent() {
		feed = append(feed, fmt.Sprintf("%s → %s %s", sanitize(msg.From), sanitize(msg.To), sanitize(msg.Kind)))
	}
	if len(feed) == 0 {
		feed = append(feed, "no messages yet")
	}
	out = append(out, sDim.Render(clip("  "+strings.Join(feed, "  ·  "), w)))
	hint := "  + - zoom  hjkl pan  0 fit  [ ] select  o orbit  enter Map  space pause  ? keys"
	if m.graphPaused {
		hint = "  paused · " + strings.TrimSpace(hint)
	}
	out = append(out, sDim.Render(clip(hint, w)))
	return out
}
func graphOrbitPositions(g *graphview.Graph) map[string]graphview.Point {
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
	out := map[string]graphview.Point{}
	for d, names := range rings {
		for i, name := range names {
			a := 2*math.Pi*float64(i)/float64(len(names)) - math.Pi/2
			radius := float64(d) * 0.65
			out[name] = graphview.Point{X: center.X + radius*math.Cos(a), Y: center.Y + radius*math.Sin(a)}
		}
	}
	return out
}
