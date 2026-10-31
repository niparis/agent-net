package graphview

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Braille renders a graph in terminal cells. Color, when provided, styles a
// cell according to its semantic role.
type Braille struct {
	Color         func(role, text string) string
	Now           time.Time
	ReducedMotion bool
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
	type arrow struct {
		x, y int
		r    rune
		role string
	}
	var arrows []arrow
	for _, e := range g.Edges {
		x0, y0, ok := point(e.From)
		x1, y1, ok2 := point(e.To)
		if !ok || !ok2 {
			continue
		}
		ax, ay := x1-x0, y1-y0
		if ax != 0 || ay != 0 {
			angle := math.Atan2(float64(ay)/4, float64(ax)/2)
			dirs := []rune{'→', '↘', '↓', '↙', '←', '↖', '↑', '↗'}
			idx := (int(math.Round(angle/(math.Pi/4))) + 8) % 8
			arrows = append(arrows, arrow{int(math.Round(float64(x0)*0.2+float64(x1)*0.8)) / 2, int(math.Round(float64(y0)*0.2+float64(y1)*0.8)) / 4, dirs[idx], e.Kind})
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
	_, flashes := g.Traffic(b.Now)
	flash := map[string]bool{}
	for _, name := range flashes {
		flash[name] = true
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
		role := "node"
		for _, sess := range n.Sessions {
			if sess.Status == "busy" {
				role = "busy"
				if !b.ReducedMotion && b.Now.UnixMilli()%1000 >= 500 {
					role = "busyPulse"
				}
				break
			}
		}
		if flash[n.Name] {
			role = "flash"
		}
		put(cx, cy, marker, role)
	}
	if x, y, ok := point(g.Selected()); ok {
		cx, cy := x/2, y/4
		if !put(cx-1, cy, '▸', "selected") {
			put(cx, cy-1, '▾', "selected")
		}
	}

	for _, n := range g.Nodes {
		x, y, _ := point(n.Name)
		cx, cy := x/2, y/4
		label := n.Name
		if cam.Level() == "cards" {
			label = "[" + label + "]"
		}
		for _, e := range g.Edges {
			if e.From == "*" && e.To == n.Name {
				label += " ◂all"
				break
			}
		}
		rs := []rune(label)
		candidates := [][2]int{{cx + 1, cy}, {cx - len(rs), cy}, {cx - len(rs)/2, cy - 1}, {cx - len(rs)/2, cy + 1}}
		placed := false
		labelX, labelY := 0, 0
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
			labelX, labelY = pos[0], pos[1]
			break
		}
		if !placed {
			for length := len(rs) - 1; length >= 2 && !placed; length-- {
				short := append(append([]rune(nil), rs[:length-1]...), '…')
				for _, pos := range [][2]int{{cx + 1, cy}, {cx - length, cy}, {cx - length/2, cy - 1}, {cx - length/2, cy + 1}} {
					if pos[0] < 0 || pos[1] < 0 || pos[0]+length > width || pos[1] >= height {
						continue
					}
					free := true
					for i := range short {
						if cells[pos[1]*width+pos[0]+i].occupied {
							free = false
							break
						}
					}
					if !free {
						continue
					}
					for i, r := range short {
						put(pos[0]+i, pos[1], r, "label")
					}
					placed = true
					labelX, labelY = pos[0], pos[1]
					break
				}
			}
		}
		if placed && cam.Level() == "cards" {
			count := max(n.SessionCount, len(n.Sessions))
			detail := fmt.Sprintf("%d sessions", count)
			if n.Charter.Purpose != "" {
				detail += " · " + n.Charter.Purpose
			}
			detailRunes := []rune(detail)
			if len(detailRunes) > width-labelX {
				detailRunes = detailRunes[:max(0, width-labelX)]
			}
			for _, row := range []int{labelY + 1, labelY - 1} {
				if row < 0 || row >= height || len(detailRunes) == 0 {
					continue
				}
				free := true
				for i := range detailRunes {
					if cells[row*width+labelX+i].occupied {
						free = false
						break
					}
				}
				if free {
					for i, r := range detailRunes {
						put(labelX+i, row, r, "card")
					}
					break
				}
			}
		}
	}
	for _, a := range arrows {
		put(a.x, a.y, a.r, a.role)
	}
	for _, c := range func() []Comet { cs, _ := g.Traffic(b.Now); return cs }() {
		a := g.Positions[c.From]
		target := g.Positions[c.To]
		for i := 3; i >= 1; i-- {
			u := c.Progress - float64(i)*0.035
			if u < 0 {
				continue
			}
			x, y := cam.Project(Point{a.X + (target.X-a.X)*u, a.Y + (target.Y-a.Y)*u}, width, height)
			dot(int(x*2), int(y*4), "trail")
		}
		x, y := cam.Project(c.Position, width, height)
		cx, cy := int(x), int(y)
		if !put(cx, cy, '•', "comet") {
			for _, offset := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				if put(cx+offset[0], cy+offset[1], '•', "comet") {
					break
				}
			}
		}
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
