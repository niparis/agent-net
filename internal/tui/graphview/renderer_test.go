package graphview

import (
	"math"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type asciiRenderer struct{}

func (asciiRenderer) Render(scene Scene, size Size) Output {
	if size.Width <= 0 || size.Height <= 0 {
		return Output{}
	}
	rows := make([][]rune, size.Height)
	for y := range rows {
		rows[y] = []rune(strings.Repeat(" ", size.Width))
	}
	for _, e := range scene.Edges {
		x0, y0 := int(e.From.X*float64(size.Width)), int(e.From.Y*float64(size.Height))
		x1, y1 := int(e.To.X*float64(size.Width)), int(e.To.Y*float64(size.Height))
		dx, dy := x1-x0, y1-y0
		steps := max(abs(dx), abs(dy))
		if steps > 10000 {
			continue
		}
		marker := '-'
		if dx == 0 {
			marker = '|'
		} else if dy != 0 {
			marker = '\\'
			if (dx < 0) != (dy < 0) {
				marker = '/'
			}
		}
		for i := 0; i <= steps; i++ {
			x, y := x0, y0
			if steps > 0 {
				x += int(math.Round(float64(dx*i) / float64(steps)))
				y += int(math.Round(float64(dy*i) / float64(steps)))
			}
			if x >= 0 && x < size.Width && y >= 0 && y < size.Height {
				rows[y][x] = marker
			}
		}
	}
	for _, n := range scene.Nodes {
		x, y := int(n.Position.X*float64(size.Width)), int(n.Position.Y*float64(size.Height))
		if y < 0 || y >= size.Height || x < 0 || x >= size.Width {
			continue
		}
		rows[y][x] = '*'
		for i, r := range []rune(n.Label) {
			if x+2+i < size.Width {
				rows[y][x+2+i] = r
			}
		}
	}
	out := Output{}
	for _, row := range rows {
		out.Lines = append(out.Lines, string(row))
	}
	return out
}
func TestRenderersUseSameSceneNodes(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "alpha"}, {Name: "beta"}}, Edges: []protocol.Edge{{From: "alpha", To: "beta", Kind: "depends"}}})
	g.Positions = map[string]Point{"alpha": {-2, -1}, "beta": {2, 1}}
	size := Size{Width: 80, Height: 20}
	scene := BuildScene(State{Graph: g, Camera: Camera{Zoom: 2, FitZoom: 2}}, Viewport{Width: 80, Height: 20})
	ascii := strings.Join((asciiRenderer{}).Render(scene, size).Lines, "\n")
	if !strings.Contains(ascii, "\\") {
		t.Errorf("ASCII edge missing: %s", ascii)
	}
	for _, renderer := range []Renderer{Braille{}, asciiRenderer{}} {
		out := strings.Join(renderer.Render(scene, size).Lines, "\n")
		for _, n := range scene.Nodes {
			if !strings.Contains(out, n.Label) {
				t.Errorf("%T did not draw %s:\n%s", renderer, n.Label, out)
			}
		}
	}
}
