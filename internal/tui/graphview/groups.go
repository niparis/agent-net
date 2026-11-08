package graphview

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Group is an overview box. Members remain independently selectable nodes;
// the header is selectable too, for team zoom. Rect is in terminal cells.
type Group struct {
	Name, Label, Lead            string
	Members                      []string
	ColumnWidths                 []int
	X, Y, Width, Height, Columns int
}

// ArrangeGroups packs boxes by their measured content, rather than assigning
// projects to fixed columns. Oversized overviews follow the selected member.
func ArrangeGroups(scene Scene, size Size) Scene {
	nodes := map[string]SceneNode{}
	for _, n := range scene.Nodes {
		nodes[n.Name] = n
	}
	// Start with compact boxes, then widen the box that most reduces the
	// overall packed height. This adapts to both the content and the viewport.
	measure := func(g *Group, columns int) {
		var normal []SceneNode
		marked, markWidth := 0, 0
		for _, name := range g.Members {
			n := nodes[name]
			if n.Wildcard {
				marked++
				markWidth = max(markWidth, ansi.StringWidth(n.WildcardLabel)+4, ansi.StringWidth(n.Label)+6)
			} else {
				normal = append(normal, n)
			}
		}
		columns = max(1, min(columns, max(len(normal), 1)))
		rows := (len(normal) + columns - 1) / columns
		widths := make([]int, columns)
		for i := range widths {
			widths[i] = 1
		}
		for j, n := range normal {
			col := j / max(rows, 1)
			widths[col] = max(widths[col], ansi.StringWidth(n.Label)+3)
		}
		width := 4
		for _, w := range widths {
			width += w
		}
		width = max(width, ansi.StringWidth(g.Label)+6, markWidth)
		if columns == 1 {
			width = min(width, max(size.Width-2, 4))
			widths[0] = min(widths[0], max(width-4, 1))
		}
		g.Width, g.Columns, g.ColumnWidths = width, columns, widths
		g.Height = max(rows+marked*2+3, 4)
	}

	pack := func(groups []Group) int {
		x, y, rowHeight := 1, 1, 0
		for i := range groups {
			g := &groups[i]
			if x > 1 && x+g.Width > size.Width {
				x = 1
				y += rowHeight + 3
				rowHeight = 0
			}
			g.X, g.Y = x, y
			x += g.Width + 2
			rowHeight = max(rowHeight, g.Height)
		}
		return y + rowHeight
	}
	for i := range scene.Groups {
		measure(&scene.Groups[i], 1)
	}
	height := pack(scene.Groups)
	for {
		bestHeight, bestIndex := height, -1
		var best Group
		for i, g := range scene.Groups {
			for columns := g.Columns + 1; columns <= len(g.Members); columns++ {
				candidate := g
				measure(&candidate, columns)
				if candidate.Width > size.Width-2 || candidate.Width == g.Width && candidate.Height == g.Height {
					continue
				}
				trial := append([]Group(nil), scene.Groups...)
				trial[i] = candidate
				h := pack(trial)
				if h < bestHeight {
					bestHeight, bestIndex, best = h, i, candidate
				}
			}
		}
		if bestIndex < 0 {
			break
		}
		scene.Groups[bestIndex] = best
		height = pack(scene.Groups)
	}
	positions := map[string]Point{}
	groupOf := map[string]string{}
	for _, g := range scene.Groups {
		positions[g.Name] = Point{float64(g.X + g.Width/2), float64(g.Y)}
		var normal, marked []string
		for _, name := range g.Members {
			if nodes[name].Wildcard {
				marked = append(marked, name)
			} else {
				normal = append(normal, name)
			}
		}
		rows := (len(normal) + g.Columns - 1) / g.Columns
		for j, name := range normal {
			col, row := 0, 0
			if rows > 0 {
				col, row = j/rows, j%rows
			}
			colX := g.X + 2
			for c := 0; c < col; c++ {
				colX += g.ColumnWidths[c]
			}
			positions[name] = Point{float64(colX), float64(g.Y + 2 + row)}
			groupOf[name] = g.Name
		}
		for j, name := range marked {
			positions[name] = Point{float64(g.X + 2), float64(g.Y + 2 + rows + j*2)}
			groupOf[name] = g.Name
		}

	}
	offset := 0
	for _, n := range scene.Nodes {
		if n.Selected {
			if p, ok := positions[n.Name]; ok && int(p.Y) > size.Height-3 {
				offset = int(p.Y) - size.Height + 3
			}
		}
	}
	for i := range scene.Groups {
		scene.Groups[i].Y -= offset
	}
	for key, p := range positions {
		p.Y -= float64(offset)
		positions[key] = p
	}
	for i := range scene.Nodes {
		n := &scene.Nodes[i]
		if p, ok := positions[n.Name]; ok {
			n.Position = Point{p.X / float64(size.Width), p.Y / float64(size.Height)}
		} else { // Dormant chip stays pinned below the boxes.
			n.Position = Point{.02, float64(size.Height-1) / float64(size.Height)}
		}
	}
	// One relationship per box pair, with kind counts retained in the label.
	type pair struct{ a, b string }
	counts := map[pair]map[string]int{}
	for _, e := range scene.Edges {
		a, b := groupOf[e.FromName], groupOf[e.ToName]
		if a == "" || b == "" || a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		p := pair{a, b}
		if counts[p] == nil {
			counts[p] = map[string]int{}
		}
		counts[p][e.Kind]++
	}
	scene.Edges = nil
	var pairs []pair
	for p := range counts {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].a != pairs[j].a {
			return pairs[i].a < pairs[j].a
		}
		return pairs[i].b < pairs[j].b
	})
	for _, p := range pairs {
		var kinds []string
		for kind := range counts[p] {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		var labels []string
		for _, kind := range kinds {
			n := counts[p][kind]
			if n > 1 {
				labels = append(labels, fmt.Sprintf("%d %s", n, kind))
			} else {
				labels = append(labels, kind)
			}
		}
		a, b := positions[p.a], positions[p.b]
		scene.Edges = append(scene.Edges, SceneEdge{FromName: p.a, ToName: p.b, From: Point{a.X / float64(size.Width), a.Y / float64(size.Height)}, To: Point{b.X / float64(size.Width), b.Y / float64(size.Height)}, Kind: strings.Join(labels, ", ")})
	}
	return scene
}

func (b Braille) renderGroups(scene Scene, size Size) Output {
	type glyph struct {
		r         rune
		role, hit string
		wide      bool
	}
	grid := make([]glyph, size.Width*size.Height)
	put := func(x, y int, text, role, hit string) {
		for _, r := range text {
			w := ansi.StringWidth(string(r))
			if w == 0 {
				continue
			}
			if x >= 0 && x+w <= size.Width && y >= 0 && y < size.Height {
				grid[y*size.Width+x] = glyph{r: r, role: role, hit: hit}
				if w == 2 {
					grid[y*size.Width+x+1] = glyph{wide: true, hit: hit}
				}
			}
			x += w
		}
	}
	groups := map[string]Group{}
	for _, g := range scene.Groups {
		groups[g.Name] = g
	}
	// Routes live in the gutters. Headers and member rows are painted last.
	for _, e := range scene.Edges {
		a, c := groups[e.FromName], groups[e.ToName]
		if a.Y == c.Y {
			left, right := a, c
			if left.X > right.X {
				left, right = right, left
			}
			x0, x1 := left.X+left.Width, right.X-1
			y := left.Y + left.Height/2
			for x := x0; x <= x1; x++ {
				put(x, y, "─", "edgeDim", "")
			}

		} else {
			top, bottom := a, c
			if top.Y > bottom.Y {
				top, bottom = bottom, top
			}
			x := min(top.X+top.Width/2, bottom.X+bottom.Width/2)
			y0, y1 := top.Y+top.Height, bottom.Y-1
			for y := y0; y <= y1; y++ {
				put(x, y, "│", "edgeDim", "")
			}

		}
	}
	labelY := 0
	for _, g := range scene.Groups {
		labelY = max(labelY, g.Y+g.Height)
	}
	labelX := 1
	for _, e := range scene.Edges {
		text := groups[e.FromName].Label + " ↔ " + groups[e.ToName].Label + ": " + e.Kind
		if labelX > 1 && labelX+ansi.StringWidth(text) > size.Width-1 {
			labelX = 1
			labelY++
		}
		put(labelX, labelY, ansi.Truncate(text, max(size.Width-labelX-1, 0), "…"), "edgeDim", "")
		labelX += ansi.StringWidth(text) + 3
	}
	nodes := map[string]SceneNode{}
	for _, n := range scene.Nodes {
		nodes[n.Name] = n
	}
	for _, g := range scene.Groups {
		role := "edgeDim"
		if nodes[g.Name].Selected {
			role = "selected"
		}
		for y := g.Y; y < g.Y+g.Height; y++ {
			put(g.X, y, strings.Repeat(" ", g.Width), "node", "")
		}
		put(g.X, g.Y, "┌"+strings.Repeat("─", max(g.Width-2, 0))+"┐", role, g.Name)
		put(g.X, g.Y+g.Height-1, "└"+strings.Repeat("─", max(g.Width-2, 0))+"┘", role, g.Name)
		for y := g.Y + 1; y < g.Y+g.Height-1; y++ {
			put(g.X, y, "│", role, g.Name)
			put(g.X+g.Width-1, y, "│", role, g.Name)
		}
		headerRole := "node"
		if nodes[g.Name].Selected {
			headerRole = "selectedLabel"
		}
		put(g.X+2, g.Y, ansi.Truncate(" "+g.Label+" ", max(g.Width-4, 0), "…"), headerRole, g.Name)
		for _, name := range g.Members {
			n := nodes[name]
			x, y := int(math.Round(n.Position.X*float64(size.Width))), int(math.Round(n.Position.Y*float64(size.Height)))
			width := g.Width - 4
			colX := g.X + 2
			for c, cw := range g.ColumnWidths {
				if x == colX {
					width = g.ColumnWidths[c]
					break
				}
				colX += cw
			}
			if n.Wildcard {
				width = g.Width - 4
			}
			marker := "  "
			if name == g.Lead {
				marker = "★ "
			}
			if n.Selected && marker == "  " {
				marker = "› "
			}
			labelRole := n.Role
			if labelRole == "idle" {
				labelRole = "node"
			}
			if n.Selected {
				labelRole = "selectedLabel"
			}
			text := ansi.Truncate(marker+n.Label, max(width, 1), "…")
			put(x, y, text, labelRole, name)
			if n.LabelSuffix != "" {
				start := ansi.StringWidth(marker+n.Label) - ansi.StringWidth(n.LabelSuffix)
				if start < ansi.StringWidth(text) {
					put(x+start, y, ansi.Cut(text, start, ansi.StringWidth(text)), "labelDim", name)
				}
			}
			if n.Wildcard {
				put(x, y+1, ansi.Truncate(n.WildcardLabel, max(g.Width-4, 1), "…"), "offline", name)
			}
		}
	}
	for _, n := range scene.Nodes {
		if n.Kind != "dormant" {
			continue
		}
		role := "offline"
		if n.Selected {
			role = "selectedLabel"
		}
		put(1, size.Height-1, ansi.Truncate(n.Label, max(size.Width-2, 0), "…"), role, n.Name)
	}
	out := Output{Hits: map[Cell]string{}, Labels: map[Cell]LabelCell{}}
	for y := 0; y < size.Height; y++ {
		var line strings.Builder
		for x := 0; x < size.Width; x++ {
			c := grid[y*size.Width+x]
			if c.hit != "" {
				out.Hits[Cell{x, y}] = c.hit
			}
			if c.wide {
				continue
			}
			if c.r == 0 {
				c.r = ' '
			}
			text := string(c.r)
			if b.Color != nil {
				text = b.Color(c.role, text)
			} else if b.Styled {
				text = StyleRole(c.role, text)
			}
			line.WriteString(text)
			out.Labels[Cell{x, y}] = LabelCell{c.r, c.role}
		}
		out.Lines = append(out.Lines, line.String())
	}
	return out
}
