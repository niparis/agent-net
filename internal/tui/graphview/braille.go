package graphview

import (
	"math"
	"strings"
)

// Braille renders a graph in terminal cells. Color, when provided, styles a
// cell according to its semantic role.
type Braille struct {
	Color func(role, text string) string
}
type cell struct {
	dots     byte
	text     rune
	role     string
	occupied bool
}

func (b Braille) Render(g *Graph, cam Camera, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	cells := make([]cell, width*height)
	put := func(x, y int, r rune, role string) bool {
		if x < 0 || y < 0 || x >= width || y >= height {
			return false
		}
		c := &cells[y*width+x]
		if c.occupied {
			return false
		}
		c.text = r
		c.role = role
		c.occupied = true
		return true
	}
	dot := func(x, y int, role string) {
		if x < 0 || y < 0 || x >= width*2 || y >= height*4 {
			return
		}
		c := &cells[(y/4)*width+x/2]
		bits := [2][4]byte{{1, 2, 4, 64}, {8, 16, 32, 128}}
		c.dots |= bits[x%2][y%4]
		c.role = role
	}
	point := func(name string) (int, int, bool) {
		p, ok := g.Positions[name]
		x, y := cam.Project(p, width, height)
		return int(math.Round(x * 2)), int(math.Round(y * 4)), ok
	}
	for _, e := range g.Edges {
		x0, y0, ok := point(e.From)
		x1, y1, ok2 := point(e.To)
		if !ok || !ok2 {
			continue
		}
		dx := abs(x1 - x0)
		dy := -abs(y1 - y0)
		sx, sy := -1, -1
		if x0 < x1 {
			sx = 1
		}
		if y0 < y1 {
			sy = 1
		}
		err := dx + dy
		// Prevent an extreme camera pan from spending unbounded time off-screen.
		if dx+(-dy) > 10000 {
			continue
		}
		for step := 0; ; step++ {
			if e.Kind != "governs" || step%8 < 5 {
				dot(x0, y0, e.Kind)
			}
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
	for _, n := range g.Nodes {
		x, y, _ := point(n.Name)
		cx, cy := x/2, y/4
		marker := '○'
		if n.Kind == "architect" {
			marker = '◆'
		} else if n.SessionCount > 0 || len(n.Sessions) > 0 {
			marker = '●'
		}
		put(cx, cy, marker, "node")
	}
	for _, n := range g.Nodes {
		x, y, _ := point(n.Name)
		cx, cy := x/2, y/4
		label := n.Name
		for _, e := range g.Edges {
			if e.From == "*" && e.To == n.Name {
				label += " ◂all"
				break
			}
		}
		rs := []rune(label)
		candidates := [][2]int{{cx + 2, cy}, {cx - len(rs) - 1, cy}, {cx - len(rs)/2, cy - 1}, {cx - len(rs)/2, cy + 1}}
		placed := false
		for _, pos := range candidates {
			if pos[0] < 0 || pos[1] < 0 || pos[0]+len(rs) > width || pos[1] >= height {
				continue
			}
			free := true
			for i := range rs {
				if cells[pos[1]*width+pos[0]+i].occupied {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			for i, r := range rs {
				put(pos[0]+i, pos[1], r, "label")
			}
			placed = true
			break
		}
		_ = placed
	}
	out := make([]string, height)
	for y := 0; y < height; y++ {
		var s strings.Builder
		for x := 0; x < width; x++ {
			c := cells[y*width+x]
			r := c.text
			if !c.occupied {
				if c.dots > 0 {
					r = rune(0x2800 + int(c.dots))
				} else {
					r = ' '
				}
			}
			text := string(r)
			if b.Color != nil && r != ' ' {
				text = b.Color(c.role, text)
			}
			s.WriteString(text)
		}
		out[y] = s.String()
	}
	return out
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
