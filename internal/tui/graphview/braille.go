package graphview

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Braille renders a graph in terminal cells. Color, when provided, styles a
// cell according to its semantic role.
type Braille struct {
	Color  func(role, text string) string
	Styled bool
}
type cell struct {
	dots     byte
	text     rune
	role     string
	occupied bool
}

func (b Braille) Render(scene Scene, size Size) Output {
	width, height := size.Width, size.Height
	if width <= 0 || height <= 0 {
		return Output{}
	}
	cells := make([]cell, width*height)
	hits := make(map[Cell]string)
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
	point := func(p Point) (int, int) {
		return int(math.Round(p.X * float64(width) * 2)), int(math.Round(p.Y * float64(height) * 4))
	}
	type arrow struct {
		x, y int
		r    rune
		role string
	}
	var arrows []arrow
	// Paint ordinary talk below relationships and voice above them.
	drawTalk := func(talk SceneTalk) {
		x0, y0 := point(talk.From)
		x1, y1 := point(talk.To)
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
		role := "talkFaint"
		if talk.NoRule {
			role = "talk"
		}
		if talk.Voice {
			role = "voice"
		}
		spacing := 8
		if talk.Bucket == 2 {
			spacing = 5
		} else if talk.Bucket >= 3 {
			spacing = 3
		}
		err := dx + dy
		for step := 0; ; step++ {
			if step%spacing == 0 {
				dot(x0, y0, role)
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
	for _, talk := range scene.Talk {
		if !talk.Voice {
			drawTalk(talk)
		}
	}

	for _, e := range scene.Edges {
		x0, y0 := point(e.From)
		x1, y1 := point(e.To)
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
			if !e.Dashed || step%8 < 5 {
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
	for _, talk := range scene.Talk {
		if talk.Voice {
			drawTalk(talk)
		}
	}

	for _, n := range scene.Nodes {
		x, y := point(n.Position)
		marker := '○'
		if n.HasCharter {
			marker = '●'
		}
		role := "node"
		if n.Role == "busy" {
			role = "busy"
			if n.Pulse {
				role = "busyPulse"
			}
		}
		if n.Flashing {
			role = "flash"
		}
		if put(x/2, y/4, marker, role) {
			hits[Cell{x / 2, y / 4}] = n.Name
		}
	}
	// Reserve each disc's cells before labels are placed. Earlier discs win
	// where nodes overlap, and marker cells always remain visible.
	for _, n := range scene.Nodes {
		x, y := point(n.Position)
		cx, cy := x/2, y/4
		touched := map[Cell]bool{}
		// Fill a compact two-row halo in sub-cell steps around the marker.
		offsets := [][2]int{{-1, 0}, {1, 0}, {0, -1}, {-1, -1}, {1, -1}}
		for i := 0; i < max(1, min(n.Activity, 5))-1; i++ {
			px, py := cx+offsets[i][0], cy+offsets[i][1]
			if px < 0 || py < 0 || px >= width || py >= height || cells[py*width+px].occupied {
				continue
			}
			for d := 0; d < 4; d++ {
				dot(px*2+d%2, py*4+1+d/2, "node")
			}
			touched[Cell{px, py}] = true
		}
		for c := range touched {
			cell := &cells[c.Y*width+c.X]
			cell.text = rune(0x2800 + int(cell.dots))
			cell.occupied = true
			hits[c] = n.Name
		}
	}
	for _, n := range scene.Nodes {
		if !n.Selected {
			continue
		}
		x, y := point(n.Position)
		cx, cy := x/2, y/4
		if !put(cx-2, cy, '▸', "selected") {
			put(cx, cy-2, '▾', "selected")
		}
	}

	labelNodes := append([]SceneNode(nil), scene.Nodes...)
	sort.SliceStable(labelNodes, func(i, j int) bool { return labelNodes[i].Selected && !labelNodes[j].Selected })
	for _, n := range labelNodes {
		x, y := point(n.Position)
		cx, cy := x/2, y/4
		label := n.Label
		if label == "" {
			continue
		}
		if scene.Level == "cards" {
			label = "[" + label + "]"
		}
		if n.Wildcard {
			label += " ◂all"
		}
		labelRole := "labelDim"
		if n.HasCharter {
			labelRole = "label"
		}
		rs := []rune(label)
		left, right, top := 0, 0, 0
		if n.Activity >= 2 {
			left = 1
		}
		if n.Activity >= 3 {
			right = 1
		}
		if n.Activity >= 4 {
			top = 1
		}
		candidates := [][2]int{{cx + right + 2, cy}, {cx - len(rs) - left - 1, cy}, {cx - len(rs)/2, cy - top - 1}, {cx - len(rs)/2, cy + 1}}
		// A selected node gets another chance in any free row of the viewport.
		if n.Selected {
			for row := 0; row < height; row++ {
				for col := 0; col+len(rs) <= width; col++ {
					candidates = append(candidates, [2]int{col, row})
				}
			}
		}
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
				if put(pos[0]+i, pos[1], r, labelRole) {
					hits[Cell{pos[0] + i, pos[1]}] = n.Name
				}
			}
			// Keep the cell between a marker and a label on its row blank,
			// even when an edge or arrow passes through it.
			if pos[1] == cy {
				gap := pos[0] - 1
				if pos[0] < cx {
					gap = pos[0] + len(rs)
				}
				if gap >= 0 && gap < width && !cells[cy*width+gap].occupied {
					cells[cy*width+gap] = cell{text: ' ', role: "label", occupied: true}
				}
			}
			placed = true
			labelX, labelY = pos[0], pos[1]
			break
		}

		if placed && scene.Level == "cards" {
			count := n.Card.Sessions
			detail := fmt.Sprintf("%d sessions", count)
			if n.Card.Purpose != "" {
				detail += " · " + n.Card.Purpose
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
	for _, c := range scene.Comets {
		role, trailRole := "comet", "trail"
		if c.Voice {
			role, trailRole = "voice", "voice"
		}
		for _, p := range c.Trail {
			dot(int(p.X*float64(width)*2), int(p.Y*float64(height)*4), trailRole)
		}
		cx, cy := int(c.Position.X*float64(width)), int(c.Position.Y*float64(height))
		if !put(cx, cy, '•', role) {
			for _, offset := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				if put(cx+offset[0], cy+offset[1], '•', role) {
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
			if r != ' ' {
				if b.Color != nil {
					text = b.Color(c.role, text)
				} else if b.Styled {
					text = colorRole(c.role, text)
				}
			}
			s.WriteString(text)
		}
		out[y] = s.String()
	}
	return Output{Lines: out, Hits: hits}
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// colorRole owns the terminal palette for semantic drawing roles.
func colorRole(role, text string) string {
	color := lipgloss.AdaptiveColor{Light: "#596171", Dark: "#6f7889"}
	switch role {
	case "voice":
		color = voiceColor
	case "talk":
		color = talkColor
	case "talkFaint":
		color = talkFaintColor
	case "depends", "trail":
		color = lipgloss.AdaptiveColor{Light: "#00666b", Dark: "#5fc4c9"}
	case "governs":
		color = lipgloss.AdaptiveColor{Light: "#6445a1", Dark: "#b49cf5"}
	case "deploys", "selected", "flash", "busyPulse":
		color = lipgloss.AdaptiveColor{Light: "#865000", Dark: "#f0b35a"}
	case "node", "label", "card":
		color = lipgloss.AdaptiveColor{Light: "#242a34", Dark: "#d7dce5"}
	case "busy", "comet":
		color = lipgloss.AdaptiveColor{Light: "#246b32", Dark: "#7ccf8a"}
	}
	return lipgloss.NewStyle().Foreground(color).Render(text)
}
