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
	width, height := size.Width, size.Height
	// Draw the node markers first so edges pass beneath them, then lay the
	// team frames and their text over the same grid.
	base := scene
	base.TeamCanvas = false
	base.Groups = nil
	base.Frames = nil
	base.Texts = nil
	base.Edges = nil
	base.Nodes = append([]SceneNode(nil), scene.Nodes...)
	for i := range base.Nodes {
		base.Nodes[i].Label = ""
		base.Nodes[i].ShowCard = false
	}
	raw := (Braille{}).Render(base, size)
	grid := make([][]rune, height)
	for y := range grid {
		grid[y] = []rune(raw.Lines[y])
		for len(grid[y]) < width {
			grid[y] = append(grid[y], ' ')
		}
	}
	roles := map[Cell]string{}
	dots := make([]byte, width*height)
	dotRoles := map[Cell]string{}
	bits := [2][4]byte{{1, 2, 4, 64}, {8, 16, 32, 128}}
	dot := func(x, y int, role string) {
		if x < 0 || y < 0 || x >= width*2 || y >= height*4 {
			return
		}
		c := (y/4)*width + x/2
		dots[c] |= bits[x%2][y%4]
		if dotRoles[Cell{x / 2, y / 4}] == "" {
			dotRoles[Cell{x / 2, y / 4}] = role
		}
	}
	lineDots := func(x0, y0, x1, y1 int, role string) {
		dx, dy := abs(x1-x0), -abs(y1-y0)
		if dx-dy > 10000 {
			return
		}
		sx, sy := -1, -1
		if x0 < x1 {
			sx = 1
		}
		if y0 < y1 {
			sy = 1
		}
		err := dx + dy
		for {
			dot(x0, y0, role)
			if x0 == x1 && y0 == y1 {
				break
			}
			v := 2 * err
			if v >= dy {
				err += dy
				x0 += sx
			}
			if v <= dx {
				err += dx
				y0 += sy
			}
		}
	}
	type arrow struct {
		x, y int
		r    rune
		role string
	}
	var arrows []arrow
	edgeRole := func(e SceneEdge) string {
		if e.Selected {
			return "selectedEdge"
		}
		if e.Dimmed {
			return "edgeDim"
		}
		return e.Kind
	}
	for _, e := range scene.Edges {
		pts := e.Path
		if len(pts) < 2 {
			ctrl := bendControl(e.From, e.To, .18)
			pts = sampleQuad(e.From, ctrl, e.To, 32)
		}
		role := edgeRole(e)
		for i := 1; i < len(pts); i++ {
			a, z := pts[i-1], pts[i]
			x0, y0 := int(math.Round(a.X*float64(width)*2)), int(math.Round(a.Y*float64(height)*4))
			x1, y1 := int(math.Round(z.X*float64(width)*2)), int(math.Round(z.Y*float64(height)*4))
			lineDots(x0, y0, x1, y1, role)
		}
		// One arrow, next to the target.
		end, prev := pts[len(pts)-1], pts[len(pts)-2]
		dx := (end.X - prev.X) * float64(width)
		dy := (end.Y - prev.Y) * float64(height)
		if dx != 0 || dy != 0 {
			angle := math.Atan2(dy, dx)
			dirs := []rune{'→', '↘', '↓', '↙', '←', '↖', '↑', '↗'}
			idx := (int(math.Round(angle/(math.Pi/4))) + 8) % 8
			step := [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
			ex := int(math.Round(end.X * float64(width)))
			ey := int(math.Round(end.Y * float64(height)))
			arrows = append(arrows, arrow{ex - step[idx][0], ey - step[idx][1], dirs[idx], role})
		}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*width + x
			if dots[i] == 0 || grid[y][x] != ' ' {
				continue
			}
			grid[y][x] = rune(0x2800 + int(dots[i]))
			roles[Cell{x, y}] = dotRoles[Cell{x, y}]
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
			grid[f.Y][x] = '─'
			grid[f.Y+f.Height-1][x] = '─'
			roles[Cell{x, f.Y}] = role
			roles[Cell{x, f.Y + f.Height - 1}] = role
		}
		for y := f.Y + 1; y < f.Y+f.Height-1; y++ {
			grid[y][f.X] = '│'
			grid[y][f.X+f.Width-1] = '│'
			roles[Cell{f.X, y}] = role
			roles[Cell{f.X + f.Width - 1, y}] = role
		}
		grid[f.Y][f.X] = '╭'
		grid[f.Y][f.X+f.Width-1] = '╮'
		grid[f.Y+f.Height-1][f.X] = '╰'
		grid[f.Y+f.Height-1][f.X+f.Width-1] = '╯'
		roles[Cell{f.X, f.Y}] = role
		roles[Cell{f.X + f.Width - 1, f.Y}] = role
		roles[Cell{f.X, f.Y + f.Height - 1}] = role
		roles[Cell{f.X + f.Width - 1, f.Y + f.Height - 1}] = role
	}
	for _, a := range arrows {
		if a.x >= 0 && a.x < width && a.y >= 0 && a.y < height && grid[a.y][a.x] == ' ' {
			grid[a.y][a.x] = a.r
			roles[Cell{a.x, a.y}] = a.role
		}
	}
	labels, hits := teamText(scene, size)
	for p, c := range labels {
		if p.X < 0 || p.X >= width || p.Y < 0 || p.Y >= height {
			continue
		}
		grid[p.Y][p.X] = c.Rune
		roles[p] = c.Role
		if ansi.StringWidth(string(c.Rune)) == 2 && p.X+1 < width {
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
