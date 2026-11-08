package graphview

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/vector"
)

// teamFrame draws the translucent rounded team boxes used by the overview.
func (c *pixelCanvas) teamFrame(f SceneFrame) {
	if f.Card {
		c.cardBox(image.Rect(f.X, f.Y, f.X+f.Width, f.Y+f.Height), teamColor(f.Role))
		return
	}
	x0, y0 := float64(f.X*c.metrics.CellWidth)+4, float64(f.Y*c.metrics.CellHeight)+4
	x1, y1 := float64((f.X+f.Width)*c.metrics.CellWidth)-4, float64((f.Y+f.Height)*c.metrics.CellHeight)-4
	if x1 <= x0 || y1 <= y0 {
		return
	}
	box := image.Rect(int(x0)-2, int(y0)-2, int(x1)+3, int(y1)+3).Intersect(c.img.Bounds())
	if box.Empty() {
		return
	}
	ox, oy := float64(box.Min.X), float64(box.Min.Y)
	col := teamColor(f.Role)
	if f.Selected {
		col = pixelAccent
	}
	width, alpha, fill := 1.2, uint8(0x70), uint8(0x10)
	if f.Selected {
		width, alpha, fill = 1.8, 0xd0, 0x18
	}
	c.solid(box, func(z *vector.Rasterizer) { roundedRectPath(z, x0-ox, y0-oy, x1-ox, y1-oy, 10) }, color.NRGBA{col.R, col.G, col.B, alpha})
	c.solid(box, func(z *vector.Rasterizer) {
		roundedRectPath(z, x0+width-ox, y0+width-oy, x1-width-ox, y1-width-oy, 10-width)
	}, color.NRGBA{0x13, 0x18, 0x21, 0xff})
	c.solid(box, func(z *vector.Rasterizer) {
		roundedRectPath(z, x0+width-ox, y0+width-oy, x1-width-ox, y1-width-oy, 10-width)
	}, color.NRGBA{col.R, col.G, col.B, fill})
}

func teamColor(role string) color.NRGBA {
	switch role {
	case "busy":
		return pixelBusy
	case "needs you":
		return deploysColor
	case "idle":
		return ringMature
	case "selected":
		return pixelAccent
	default:
		return ringNone
	}
}

// Text and hit positions are shared by the pixel and Braille renderers.
func teamText(scene Scene, size Size) (map[Cell]LabelCell, map[Cell]string) {
	labels, hits := map[Cell]LabelCell{}, map[Cell]string{}
	for _, f := range scene.Frames {
		if f.Hit == "" {
			continue
		}
		for y := max(0, f.Y); y < min(size.Height, f.Y+f.Height); y++ {
			for x := max(0, f.X); x < min(size.Width, f.X+f.Width); x++ {
				hits[Cell{x, y}] = f.Hit
			}
		}
	}
	for _, t := range scene.Texts {
		x := t.X
		for _, r := range t.Text {
			w := ansi.StringWidth(string(r))
			if w <= 0 {
				continue
			}
			if x >= 0 && x+w <= size.Width && t.Y >= 0 && t.Y < size.Height {
				labels[Cell{x, t.Y}] = LabelCell{Rune: r, Role: t.Role}
				if t.Hit != "" {
					for j := 0; j < w; j++ {
						hits[Cell{x + j, t.Y}] = t.Hit
					}
				}
			}
			x += w
		}
	}
	return labels, hits
}

func (b Braille) renderTeamCanvas(scene Scene, size Size) Output {
	if size.Width <= 0 || size.Height <= 0 {
		return Output{}
	}
	// Use the usual curved edges and markers, then lay the same frame and text
	// cells over them. Routed overview links use gutter-only straight segments.
	base := scene
	base.TeamCanvas = false
	base.Groups = nil
	base.Frames = nil
	base.Texts = nil
	base.Edges = nil
	for _, e := range scene.Edges {
		if len(e.Path) == 0 {
			base.Edges = append(base.Edges, e)
		}
	}
	base.Nodes = append([]SceneNode(nil), scene.Nodes...)
	for i := range base.Nodes {
		base.Nodes[i].Label = ""
		base.Nodes[i].ShowCard = false
	}
	raw := (Braille{}).Render(base, size)
	grid := make([][]rune, size.Height)
	roles := map[Cell]string{}
	for y := range grid {
		grid[y] = []rune(raw.Lines[y])
		for len(grid[y]) < size.Width {
			grid[y] = append(grid[y], ' ')
		}
	}
	put := func(x, y int, r rune, role string) {
		if x >= 0 && x < size.Width && y >= 0 && y < size.Height {
			grid[y][x] = r
			roles[Cell{x, y}] = role
		}
	}
	for _, f := range scene.Frames {
		if f.Width < 2 || f.Height < 2 {
			continue
		}
		role := "edgeDim"
		if f.Selected {
			role = "selected"
		}
		for x := f.X + 1; x < f.X+f.Width-1; x++ {
			put(x, f.Y, '─', role)
			put(x, f.Y+f.Height-1, '─', role)
		}
		for y := f.Y + 1; y < f.Y+f.Height-1; y++ {
			put(f.X, y, '│', role)
			put(f.X+f.Width-1, y, '│', role)
		}
		put(f.X, f.Y, '╭', role)
		put(f.X+f.Width-1, f.Y, '╮', role)
		put(f.X, f.Y+f.Height-1, '╰', role)
		put(f.X+f.Width-1, f.Y+f.Height-1, '╯', role)
	}
	for _, e := range scene.Edges {
		for i := 1; i < len(e.Path); i++ {
			a, z := e.Path[i-1], e.Path[i]
			x0, y0 := int(math.Round(a.X*float64(size.Width))), int(math.Round(a.Y*float64(size.Height)))
			x1, y1 := int(math.Round(z.X*float64(size.Width))), int(math.Round(z.Y*float64(size.Height)))
			if x0 == x1 {
				for y := min(y0, y1) + 1; y < max(y0, y1); y++ {
					put(x0, y, '│', e.Kind)
				}
			} else if y0 == y1 {
				for x := min(x0, x1) + 1; x < max(x0, x1); x++ {
					put(x, y0, '─', e.Kind)
				}
			}
		}
	}
	labels, hits := teamText(scene, size)
	for p, c := range labels {
		put(p.X, p.Y, c.Rune, c.Role)
		if ansi.StringWidth(string(c.Rune)) == 2 && p.X+1 < size.Width {
			grid[p.Y][p.X+1] = 0
		}
	}
	for p, h := range raw.Hits {
		if hits[p] == "" {
			hits[p] = h
		}
	}
	out := Output{Hits: hits, Labels: labels}
	for y := range grid {
		var line strings.Builder
		for x, r := range grid[y] {
			if r == 0 {
				continue
			}
			s := string(r)
			if b.Color != nil {
				s = b.Color(roles[Cell{x, y}], s)
			} else if b.Styled && r != ' ' {
				s = StyleRole(roles[Cell{x, y}], s)
			}
			line.WriteString(s)
		}
		out.Lines = append(out.Lines, line.String())
	}
	return out
}
