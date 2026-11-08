package graphview

import (
	"image"
	"image/color"
	"math"
	"sort"
	"strings"

	"golang.org/x/image/vector"
)

// cardBox is the cell area and text of a zoom-in charter card. The text is
// drawn as terminal cells; the box itself is drawn in pixels.
type cardBox struct {
	Node  int
	Cell  image.Rectangle
	Lines []string
}

const (
	cardMaxText = 26
	cardMinText = 10
)

// sceneCards lays out the charter cards for the nodes that show one. The box
// sits to the right of the node, or to its left when there is no room.
func sceneCards(scene Scene, size Size) []cardBox {
	if size.Width < 10 || size.Height < 5 {
		return nil
	}
	maxText := cardMaxText
	if room := size.Width - 6; room < maxText {
		maxText = room
	}
	if maxText < cardMinText {
		return nil
	}
	var boxes []cardBox
	for i, n := range scene.Nodes {
		if !n.ShowCard || !n.HasCharter {
			continue
		}
		if n.Position.X < 0 || n.Position.X >= 1 || n.Position.Y < 0 || n.Position.Y >= 1 {
			continue
		}
		maturity := strings.ToUpper(maturityLevel(n.Card.Maturity))
		if maturity == "" {
			maturity = "UNSET"
		}
		first := maturity + " · " + sessionCount(n.Card.Sessions)
		lines := []string{clipRunes(first, maxText)}
		for _, line := range wrapRunes(n.Card.Purpose, maxText, 2) {
			lines = append(lines, line)
		}
		width := 0
		for _, line := range lines {
			width = max(width, len([]rune(line)))
		}
		width += 2
		height := len(lines) + 2
		cx := int(math.Round(n.Position.X * float64(size.Width)))
		cy := int(math.Round(n.Position.Y * float64(size.Height)))
		x0 := cx + 3
		if x0+width > size.Width-1 {
			x0 = cx - 3 - width
		}
		if x0 < 1 {
			x0 = 1
		}
		if x0+width > size.Width-1 {
			width = size.Width - 1 - x0
		}
		y0 := cy - height/2
		if y0 < 1 {
			y0 = 1
		}
		if y0+height > size.Height-1 {
			y0 = size.Height - 1 - height
		}
		if width < 6 || height > size.Height-2 {
			continue
		}
		boxes = append(boxes, cardBox{Node: i, Cell: image.Rect(x0, y0, x0+width, y0+height), Lines: lines})
	}
	return boxes
}

func sessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return itoa(n) + " sessions"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

func clipRunes(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:max(width, 0)])
	}
	return string(r[:width-1]) + "…"
}

// wrapRunes wraps words to width and stops after maxLines. A line cut by the
// limit ends with an ellipsis.
func wrapRunes(text string, width, maxLines int) []string {
	words := strings.Fields(text)
	if len(words) == 0 || width <= 0 || maxLines <= 0 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range words {
		w := word
		if runeLen(w) > width {
			w = clipRunes(word, width)
		}
		candidate := w
		if line != "" {
			candidate = line + " " + w
		}
		if runeLen(candidate) <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
			line = ""
			if len(lines) == maxLines {
				lines[len(lines)-1] = ellipsize(lines[len(lines)-1], width)
				return lines
			}
		}
		line = w
	}
	if line != "" && len(lines) < maxLines {
		lines = append(lines, line)
	}
	return lines
}

func ellipsize(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		if width <= 1 {
			return string(r[:max(width, 0)])
		}
		return string(r[:width-1]) + "…"
	}
	return s + "…"
}

func runeLen(s string) int { return len([]rune(s)) }

// PixelLabels returns the cells that stay terminal text in pixel mode: the
// normal node labels plus the zoom-in charter cards.
func PixelLabels(scene Scene, size Size, cellMetrics ...PixelMetrics) map[Cell]LabelCell {
	labels, _ := pixelLabelsAndHits(scene, size, cellMetrics...)
	return labels
}

// PixelLayout lays out the terminal text and its hit cells in the render command.
func PixelLayout(scene Scene, size Size, metrics PixelMetrics) (map[Cell]LabelCell, map[Cell]string) {
	return pixelLabelsAndHits(scene, size, metrics)
}

func pixelLabelsAndHits(scene Scene, size Size, cellMetrics ...PixelMetrics) (map[Cell]LabelCell, map[Cell]string) {
	labels := map[Cell]LabelCell{}
	hits := map[Cell]string{}
	metrics := PixelMetrics{CellWidth: 8, CellHeight: 16}
	if len(cellMetrics) > 0 && cellMetrics[0].CellWidth > 0 && cellMetrics[0].CellHeight > 0 {
		metrics = cellMetrics[0]
	}
	if size.Width <= 0 || size.Height <= 0 {
		return labels, hits
	}
	blocked := map[Cell]bool{}
	for _, n := range scene.Nodes {
		cx, cy := n.Position.X*float64(size.Width*metrics.CellWidth), n.Position.Y*float64(size.Height*metrics.CellHeight)
		r := nodeRadius(n, scene.ZoomRatio) + 2
		for y := max(0, int(math.Floor((cy-r)/float64(metrics.CellHeight)))); y <= min(size.Height-1, int(math.Floor((cy+r)/float64(metrics.CellHeight)))); y++ {
			for x := max(0, int(math.Floor((cx-r)/float64(metrics.CellWidth)))); x <= min(size.Width-1, int(math.Floor((cx+r)/float64(metrics.CellWidth)))); x++ {
				blocked[Cell{x, y}] = true
			}
		}
	}
	suffixes := map[string]int{}
	for _, n := range scene.Nodes {
		suffixes[n.Name] = len([]rune(n.LabelSuffix))
	}
	place := func(text, role, name string, candidates []Cell) {
		runes := []rune(text)
		if len(runes) == 0 || len(runes) > size.Width {
			return
		}
		for _, c := range candidates {
			if c.X < 0 || c.Y < 0 || c.X+len(runes) > size.Width || c.Y >= size.Height {
				continue
			}
			free := true
			for i := range runes {
				p := Cell{c.X + i, c.Y}
				if blocked[p] {
					free = false
					break
				}
				if _, ok := labels[p]; ok {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			for i, r := range runes {
				p := Cell{c.X + i, c.Y}
				cellRole := role
				if suffixes[name] > 0 && i >= len(runes)-suffixes[name] {
					cellRole = "labelDim"
				}
				labels[p] = LabelCell{Rune: r, Role: cellRole}
				if name != "" {
					hits[p] = name
				}
			}
			return
		}
	}
	nodes := append([]SceneNode(nil), scene.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Selected && !nodes[j].Selected })
	for _, n := range nodes {
		if !n.Wildcard {
			continue
		}
		text := n.WildcardLabel
		if text == "" {
			text = "all projects depend"
		}
		cx, cy := n.Position.X*float64(size.Width*metrics.CellWidth), n.Position.Y*float64(size.Height*metrics.CellHeight)
		r := nodeRadius(n, scene.ZoomRatio) + 12
		x := max(0, min(size.Width-len([]rune(text)), int(cx/float64(metrics.CellWidth))-len([]rune(text))/2))
		y := int(math.Floor((cy-r)/float64(metrics.CellHeight))) - 1
		var candidates []Cell
		for row := y; row >= 0; row-- {
			candidates = append(candidates, Cell{x, row})
		}
		place(text, "label", "", candidates)
	}
	for _, n := range nodes {
		if n.Position.X < 0 || n.Position.X >= 1 || n.Position.Y < 0 || n.Position.Y >= 1 {
			continue
		}
		cx, cy := n.Position.X*float64(size.Width*metrics.CellWidth), n.Position.Y*float64(size.Height*metrics.CellHeight)
		r := nodeRadius(n, scene.ZoomRatio) + 3
		x, y := int(cx/float64(metrics.CellWidth)), int(cy/float64(metrics.CellHeight))
		length := len([]rune(n.Label))
		right := int(math.Ceil((cx + r) / float64(metrics.CellWidth)))
		left := int(math.Floor((cx-r)/float64(metrics.CellWidth))) - length
		top := int(math.Floor((cy-r)/float64(metrics.CellHeight))) - 1
		bottom := int(math.Ceil((cy + r) / float64(metrics.CellHeight)))
		candidates := []Cell{{right, y}, {left, y}, {x - length/2, top}, {x - length/2, bottom}}
		if n.Selected {
			for row := 0; row < size.Height; row++ {
				for col := 0; col+length <= size.Width; col++ {
					candidates = append(candidates, Cell{col, row})
				}
			}
		}
		role := n.Role
		if role == "" || role == "none" {
			role = "label"
		}
		if n.Selected {
			role = "pixelSelectedLabel"
		}
		place(n.Label, role, n.Name, candidates)
	}

	w, h := float64(size.Width*metrics.CellWidth), float64(size.Height*metrics.CellHeight)
	for _, edge := range scene.Edges {
		if !edge.Selected && scene.ZoomRatio <= 1.35 {
			continue
		}
		text := edge.Kind
		if edge.Mode != "" {
			text += " · " + edge.Mode
		}
		if edge.Rule != "" {
			text += " · " + edge.Rule
		}
		runes := []rune(text)
		if len(runes) > size.Width || size.Height <= 0 {
			continue
		}
		a, b := Point{edge.From.X * w, edge.From.Y * h}, Point{edge.To.X * w, edge.To.Y * h}
		p := quadPoint(a, bendControl(a, b, .18), b, .5)
		if p.X < 0 || p.X >= w || p.Y < 0 || p.Y >= h {
			continue
		}
		x := max(0, min(size.Width-len(runes), int(p.X/float64(metrics.CellWidth))-len(runes)/2))
		y := int(p.Y / float64(metrics.CellHeight))
		if y < 0 || y >= size.Height {
			continue
		}
		row := -1
		for _, offset := range []int{0, -1, 1, -2, 2} {
			candidate := y + offset
			if candidate < 0 || candidate >= size.Height {
				continue
			}
			free := true
			for i := range runes {
				if _, ok := labels[Cell{x + i, candidate}]; ok || blocked[Cell{x + i, candidate}] {
					free = false
					break
				}
			}
			if free {
				row = candidate
				break
			}
		}
		if row < 0 {
			continue
		}
		y = row
		for i, r := range runes {
			labels[Cell{x + i, y}] = LabelCell{Rune: r, Role: "label"}
		}
	}
	for _, box := range sceneCards(scene, size) {
		for y := box.Cell.Min.Y; y < box.Cell.Max.Y; y++ {
			for x := box.Cell.Min.X; x < box.Cell.Max.X; x++ {
				delete(labels, Cell{X: x, Y: y})
				delete(hits, Cell{X: x, Y: y})
			}
		}
		for i, line := range box.Lines {
			x := box.Cell.Min.X + 1
			y := box.Cell.Min.Y + 1 + i
			for _, r := range line {
				if x >= box.Cell.Max.X-1 {
					break
				}
				if r != ' ' {
					labels[Cell{X: x, Y: y}] = LabelCell{Rune: r, Role: "card"}
				}
				x++
			}
		}
	}
	return labels, hits
}

func (c *pixelCanvas) cardBox(cell image.Rectangle, ring color.NRGBA) {
	if cell.Empty() {
		return
	}
	x0 := float64(cell.Min.X * c.metrics.CellWidth)
	y0 := float64(cell.Min.Y * c.metrics.CellHeight)
	x1 := float64(cell.Max.X * c.metrics.CellWidth)
	y1 := float64(cell.Max.Y * c.metrics.CellHeight)
	bbox := image.Rect(int(math.Floor(x0))-1, int(math.Floor(y0))-1, int(math.Ceil(x1))+2, int(math.Ceil(y1))+2).Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	ox, oy := float64(bbox.Min.X), float64(bbox.Min.Y)
	border := color.NRGBA{ring.R, ring.G, ring.B, 0xcc}
	fill := color.NRGBA{0x16, 0x1c, 0x26, 0xf2}
	radius := 5.0
	c.solid(bbox, func(z *vector.Rasterizer) {
		roundedRectPath(z, x0-ox, y0-oy, x1-ox, y1-oy, radius)
	}, border)
	inset := 1.4
	c.solid(bbox, func(z *vector.Rasterizer) {
		roundedRectPath(z, x0+inset-ox, y0+inset-oy, x1-inset-ox, y1-inset-oy, math.Max(radius-inset, 1))
	}, fill)
}

func roundedRectPath(z *vector.Rasterizer, x0, y0, x1, y1, r float64) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	r = math.Min(r, math.Min((x1-x0)/2, (y1-y0)/2))
	const k = .5522847498307936
	kr := k * r
	z.MoveTo(float32(x0+r), float32(y0))
	z.LineTo(float32(x1-r), float32(y0))
	z.CubeTo(float32(x1-r+kr), float32(y0), float32(x1), float32(y0+r-kr), float32(x1), float32(y0+r))
	z.LineTo(float32(x1), float32(y1-r))
	z.CubeTo(float32(x1), float32(y1-r+kr), float32(x1-r+kr), float32(y1), float32(x1-r), float32(y1))
	z.LineTo(float32(x0+r), float32(y1))
	z.CubeTo(float32(x0+r-kr), float32(y1), float32(x0), float32(y1-r+kr), float32(x0), float32(y1-r))
	z.LineTo(float32(x0), float32(y0+r))
	z.CubeTo(float32(x0), float32(y0+r-kr), float32(x0+r-kr), float32(y0), float32(x0+r), float32(y0))
	z.ClosePath()
}
