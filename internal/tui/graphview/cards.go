package graphview

import (
	"image"
	"image/color"
	"math"
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
func PixelLabels(scene Scene, size Size) map[Cell]LabelCell {
	labels := (Braille{}).Render(scene, size).Labels
	if labels == nil {
		labels = map[Cell]LabelCell{}
	}
	for _, box := range sceneCards(scene, size) {
		for y := box.Cell.Min.Y; y < box.Cell.Max.Y; y++ {
			for x := box.Cell.Min.X; x < box.Cell.Max.X; x++ {
				delete(labels, Cell{X: x, Y: y})
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
	return labels
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
