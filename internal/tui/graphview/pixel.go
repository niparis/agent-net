package graphview

// The pixel renderer draws a Scene to an RGBA image with anti-aliased shapes.
// It is pure Go: shapes are rasterized with golang.org/x/image/vector, so the
// four release binaries still build without cgo. The terminal turns the image
// into cells with the kitty graphics protocol in kitty.go.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"math"
	"strings"

	"golang.org/x/image/vector"
)

// PixelMetrics is the size of one terminal cell in pixels.
type PixelMetrics struct{ CellWidth, CellHeight int }

const (
	// MaxPixelWidth and MaxPixelHeight cap the image. A larger image is costly
	// to rasterize, PNG-encode and transmit; the kitty protocol scales the
	// smaller image to the cell area.
	MaxPixelWidth  = 1600
	MaxPixelHeight = 900
)

// Look of the approved mockup.
var (
	pixelBackground = color.NRGBA{0x11, 0x15, 0x1c, 0xff}
	pixelCenter     = color.NRGBA{0x15, 0x1b, 0x24, 0xff}
	nodeFillCenter  = color.NRGBA{0x27, 0x31, 0x40, 0xff}
	nodeFillEdge    = color.NRGBA{0x16, 0x1c, 0x26, 0xff}
	ringMature      = color.NRGBA{0x7a, 0xa7, 0xf5, 0xff}
	ringGrowing     = color.NRGBA{0x7c, 0xcf, 0x8a, 0xff}
	ringNone        = color.NRGBA{0x6f, 0x78, 0x89, 0xff}
	architectColor  = color.NRGBA{0xb4, 0x9c, 0xf5, 0xff}
	pixelAccent     = color.NRGBA{0x5f, 0xc4, 0xc9, 0xff}
	pixelBusy       = color.NRGBA{0x7c, 0xcf, 0x8a, 0xff}
	dependsColor    = color.NRGBA{0x5f, 0xc4, 0xc9, 0xff}
	deploysColor    = color.NRGBA{0xf0, 0xb3, 0x5a, 0xff}
	talkBright      = color.NRGBA{0xff, 0x8d, 0xce, 0xff}
	talkDim         = color.NRGBA{0x80, 0x53, 0x6f, 0xff}
	talkVoice       = color.NRGBA{0xf0, 0xb3, 0x5a, 0xff}
	pixelEdgeDimmed = 0.35
)

// Rasterize draws scene into an RGBA image of size cells times metrics. It
// returns nil when the size is unknown, which makes the caller use Braille.
// Large cell areas are rasterized at a smaller size (at most MaxPixelWidth by
// MaxPixelHeight) and the terminal scales that image to the cell area.
func Rasterize(scene Scene, size Size, metrics PixelMetrics) *image.RGBA {
	if size.Width <= 0 || size.Height <= 0 || metrics.CellWidth <= 0 || metrics.CellHeight <= 0 {
		return nil
	}
	width, height := size.Width*metrics.CellWidth, size.Height*metrics.CellHeight
	if width > MaxPixelWidth || height > MaxPixelHeight {
		scale := math.Min(float64(MaxPixelWidth)/float64(width), float64(MaxPixelHeight)/float64(height))
		width = max(1, int(math.Round(float64(width)*scale)))
		height = max(1, int(math.Round(float64(height)*scale)))
		metrics.CellWidth = max(1, int(math.Round(float64(metrics.CellWidth)*scale)))
		metrics.CellHeight = max(1, int(math.Round(float64(metrics.CellHeight)*scale)))
		width = min(width, MaxPixelWidth)
		height = min(height, MaxPixelHeight)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	c := &pixelCanvas{img: img, w: float64(width), h: float64(height), level: scene.Level, metrics: metrics, zoom: scene.ZoomRatio}
	c.background()
	c.grid(scene.GridOrigin)
	for _, frame := range scene.Frames {
		c.teamFrame(frame)
	}

	selected := -1
	for i := range scene.Nodes {
		if scene.Nodes[i].Selected {
			selected = i
			break
		}
	}
	neighbours := map[int]bool{}
	if selected >= 0 {
		neighbours[selected] = true
		for _, e := range scene.Edges {
			a, b := nodeIndexAt(scene.Nodes, e.From), nodeIndexAt(scene.Nodes, e.To)
			if a == selected || b == selected {
				if a >= 0 {
					neighbours[a] = true
				}
				if b >= 0 {
					neighbours[b] = true
				}
			}
		}
		for _, t := range scene.Talk {
			a, b := nodeIndexAt(scene.Nodes, t.From), nodeIndexAt(scene.Nodes, t.To)
			if a == selected || b == selected {
				if a >= 0 {
					neighbours[a] = true
				}
				if b >= 0 {
					neighbours[b] = true
				}
			}
		}
	}

	for _, talk := range scene.Talk {
		c.talk(talk)
	}
	// Ordinary edges first, then the selection's edges, as in Braille.
	for pass := 0; pass < 2; pass++ {
		for _, e := range scene.Edges {
			if e.Selected == (pass == 1) {
				c.edge(e, scene.Nodes)
			}
		}
	}
	for i, n := range scene.Nodes {
		if i == selected {
			continue
		}
		c.node(n, scene.Level, metrics, selected >= 0 && !neighbours[i] && scene.ZoomRatio <= 1.6)
	}
	if selected >= 0 {
		c.node(scene.Nodes[selected], scene.Level, metrics, false)
	}
	for _, box := range sceneCards(scene, size) {
		c.cardBox(box.Cell, ringColor(scene.Nodes[box.Node]))
	}
	for _, comet := range scene.Comets {
		c.comet(comet)
	}
	for _, ripple := range scene.Ripples {
		c.ripple(ripple)
	}
	return img
}

// PixelHit returns the topmost node whose pixel disc covers the cell centre.
func PixelHit(scene Scene, size Size, metrics PixelMetrics, cell Cell) string {
	if metrics.CellWidth <= 0 || metrics.CellHeight <= 0 {
		return ""
	}
	x, y := (float64(cell.X)+.5)*float64(metrics.CellWidth), (float64(cell.Y)+.5)*float64(metrics.CellHeight)
	w, h := float64(size.Width*metrics.CellWidth), float64(size.Height*metrics.CellHeight)
	for i := len(scene.Nodes) - 1; i >= 0; i-- {
		n := scene.Nodes[i]
		if math.Hypot(x-n.Position.X*w, y-n.Position.Y*h) <= nodeRadius(n, scene.ZoomRatio)+2 {
			return n.Name
		}
	}
	return ""
}

// HashImage returns a stable digest of the picture, used to avoid sending an
// unchanged image to the terminal again.
func HashImage(img *image.RGBA) [32]byte {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint32(b[:4], uint32(img.Bounds().Dx()))
	binary.LittleEndian.PutUint32(b[4:], uint32(img.Bounds().Dy()))
	h.Write(b[:])
	h.Write(img.Pix)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// HashScene is a cheap digest of everything a pixel frame draws. The host
// skips rasterization when it does not change, so an idle graph costs no
// frames. Feed and key hints are left out: they are not part of the picture.
func HashScene(scene Scene, size Size) [32]byte {
	scene.Feed = nil
	scene.Hints = nil
	data, _ := json.Marshal(scene)
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint32(b[:4], uint32(size.Width))
	binary.LittleEndian.PutUint32(b[4:], uint32(size.Height))
	h.Write(b[:])
	h.Write(data)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

type pixelCanvas struct {
	img     *image.RGBA
	w, h    float64
	level   string
	metrics PixelMetrics
	zoom    float64
}

func (c *pixelCanvas) background() {
	cx, cy := c.w/2, c.h/2
	radius := math.Max(c.w, c.h) * .62
	for y := 0; y < int(c.h); y++ {
		for x := 0; x < int(c.w); x++ {
			d := math.Hypot(float64(x)+.5-cx, float64(y)+.5-cy)
			t := 1 - math.Min(d/radius, 1)
			t = t * t * (3 - 2*t)
			i := y*c.img.Stride + x*4
			c.img.Pix[i] = uint8(float64(pixelBackground.R) + float64(pixelCenter.R-pixelBackground.R)*t)
			c.img.Pix[i+1] = uint8(float64(pixelBackground.G) + float64(pixelCenter.G-pixelBackground.G)*t)
			c.img.Pix[i+2] = uint8(float64(pixelBackground.B) + float64(pixelCenter.B-pixelBackground.B)*t)
			c.img.Pix[i+3] = 255
		}
	}
}

// The grid is anchored to world zero and moves with the camera. Paint small
// antialiased disks directly to avoid thousands of vector allocations.
func (c *pixelCanvas) grid(origin Point) {
	zoom := c.zoom
	if zoom <= 0 {
		zoom = 1
	}
	step := 28 * zoom
	if step < 10 {
		return
	}
	if origin == (Point{}) {
		origin = Point{.5, .5}
	}
	ox, oy := c.px(origin)
	startX, startY := math.Mod(ox, step), math.Mod(oy, step)
	if startX < 0 {
		startX += step
	}
	if startY < 0 {
		startY += step
	}
	for cy := startY; cy < c.h; cy += step {
		for cx := startX; cx < c.w; cx += step {
			for y := int(math.Floor(cy - 1.7)); y <= int(math.Ceil(cy+1.7)); y++ {
				for x := int(math.Floor(cx - 1.7)); x <= int(math.Ceil(cx+1.7)); x++ {
					if x < 0 || y < 0 || x >= int(c.w) || y >= int(c.h) {
						continue
					}
					coverage := math.Max(0, math.Min(1, 1.7-math.Hypot(float64(x)+.5-cx, float64(y)+.5-cy))) * .10
					i := y*c.img.Stride + x*4
					c.img.Pix[i] = uint8(float64(c.img.Pix[i])*(1-coverage) + float64(ringNone.R)*coverage)
					c.img.Pix[i+1] = uint8(float64(c.img.Pix[i+1])*(1-coverage) + float64(ringNone.G)*coverage)
					c.img.Pix[i+2] = uint8(float64(c.img.Pix[i+2])*(1-coverage) + float64(ringNone.B)*coverage)
				}
			}
		}
	}
}

func (c *pixelCanvas) px(p Point) (float64, float64) { return p.X * c.w, p.Y * c.h }

// fill rasterizes a local path into dst. path works in coordinates local to
// bbox; src is sampled from its own top left corner.
func (c *pixelCanvas) fill(bbox image.Rectangle, path func(*vector.Rasterizer), src image.Image) {
	bbox = bbox.Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	z := vector.NewRasterizer(bbox.Dx(), bbox.Dy())
	path(z)
	z.Draw(c.img, bbox, src, image.Point{})
}

func (c *pixelCanvas) solid(bbox image.Rectangle, path func(*vector.Rasterizer), col color.NRGBA) {
	c.fill(bbox, path, image.NewUniform(col))
}

func (c *pixelCanvas) circle(cx, cy, r float64, col color.NRGBA) {
	if r <= 0 {
		return
	}
	bbox := image.Rect(int(math.Floor(cx-r))-1, int(math.Floor(cy-r))-1, int(math.Ceil(cx+r))+2, int(math.Ceil(cy+r))+2).Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	c.solid(bbox, func(z *vector.Rasterizer) { circlePath(z, cx-float64(bbox.Min.X), cy-float64(bbox.Min.Y), r) }, col)
}

func (c *pixelCanvas) circleGradient(cx, cy, r float64, fill func(dx, dy float64) color.NRGBA) {
	if r <= 0 {
		return
	}
	bbox := image.Rect(int(math.Floor(cx-r))-1, int(math.Floor(cy-r))-1, int(math.Ceil(cx+r))+2, int(math.Ceil(cy+r))+2).Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	c.fill(bbox, func(z *vector.Rasterizer) {
		circlePath(z, cx-float64(bbox.Min.X), cy-float64(bbox.Min.Y), r)
	}, gradient(bbox, func(x, y int) color.NRGBA {
		return fill(float64(x)+.5+float64(bbox.Min.X)-cx, float64(y)+.5+float64(bbox.Min.Y)-cy)
	}))
}

func (c *pixelCanvas) hexagonGradient(cx, cy, r float64, fill func(dx, dy float64) color.NRGBA) {
	bbox := hexBBox(cx, cy, r).Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	c.fill(bbox, func(z *vector.Rasterizer) {
		hexagonPath(z, cx, cy, r, bbox.Min)
	}, gradient(bbox, func(x, y int) color.NRGBA {
		return fill(float64(x)+.5+float64(bbox.Min.X)-cx, float64(y)+.5+float64(bbox.Min.Y)-cy)
	}))
}

func hexBBox(cx, cy, r float64) image.Rectangle {
	return image.Rect(int(math.Floor(cx-r))-1, int(math.Floor(cy-r))-1, int(math.Ceil(cx+r))+2, int(math.Ceil(cy+r))+2)
}

func hexagonPath(z *vector.Rasterizer, cx, cy, r float64, origin image.Point) {
	ox, oy := float64(origin.X), float64(origin.Y)
	for i := 0; i < 6; i++ {
		a := -math.Pi/2 + float64(i)*math.Pi/3
		x, y := cx+math.Cos(a)*r-ox, cy+math.Sin(a)*r-oy
		if i == 0 {
			z.MoveTo(float32(x), float32(y))
		} else {
			z.LineTo(float32(x), float32(y))
		}
	}
	z.ClosePath()
}

func circlePath(z *vector.Rasterizer, cx, cy, r float64) {
	const k = .5522847498307936
	kr := k * r
	z.MoveTo(float32(cx+r), float32(cy))
	z.CubeTo(float32(cx+r), float32(cy+kr), float32(cx+kr), float32(cy+r), float32(cx), float32(cy+r))
	z.CubeTo(float32(cx-kr), float32(cy+r), float32(cx-r), float32(cy+kr), float32(cx-r), float32(cy))
	z.CubeTo(float32(cx-r), float32(cy-kr), float32(cx-kr), float32(cy-r), float32(cx), float32(cy-r))
	z.CubeTo(float32(cx+kr), float32(cy-r), float32(cx+r), float32(cy-kr), float32(cx+r), float32(cy))
	z.ClosePath()
}

// circlePathReverse winds the other way, so a second circle cuts a hole when
// both paths share a nonzero fill.
func circlePathReverse(z *vector.Rasterizer, cx, cy, r float64) {
	const k = .5522847498307936
	kr := k * r
	z.MoveTo(float32(cx+r), float32(cy))
	z.CubeTo(float32(cx+r), float32(cy-kr), float32(cx+kr), float32(cy-r), float32(cx), float32(cy-r))
	z.CubeTo(float32(cx-kr), float32(cy-r), float32(cx-r), float32(cy-kr), float32(cx-r), float32(cy))
	z.CubeTo(float32(cx-r), float32(cy+kr), float32(cx-kr), float32(cy+r), float32(cx), float32(cy+r))
	z.CubeTo(float32(cx+kr), float32(cy+r), float32(cx+r), float32(cy+kr), float32(cx+r), float32(cy))
	z.ClosePath()
}

func gradient(bbox image.Rectangle, at func(x, y int) color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, bbox.Dx(), bbox.Dy()))
	for y := 0; y < bbox.Dy(); y++ {
		for x := 0; x < bbox.Dx(); x++ {
			img.SetNRGBA(x, y, at(x, y))
		}
	}
	return img
}

func nodeIndexAt(nodes []SceneNode, p Point) int {
	for i := range nodes {
		if math.Abs(nodes[i].Position.X-p.X) < 1e-9 && math.Abs(nodes[i].Position.Y-p.Y) < 1e-9 {
			return i
		}
	}
	return -1
}

func nodeRadius(n SceneNode, zoom float64) float64 {
	if n.Kind == "tag" {
		return 0
	}
	if n.Kind == "owner" || n.Kind == "cto" || n.Role == "cto" {
		return 6
	}
	if zoom <= 0 {
		zoom = 1
	}
	scale := math.Max(.75, math.Min(1.6, math.Sqrt(zoom)))
	return (9 + 4.5*math.Sqrt(float64(max(n.Card.Sessions, 0)))) * scale
}

func ringColor(n SceneNode) color.NRGBA {
	switch n.Role {
	case "needs you":
		return deploysColor
	case "busy":
		return pixelBusy
	case "idle":
		return color.NRGBA{0xd7, 0xdc, 0xe5, 0xff}
	case "offline":
		return ringNone
	}
	if n.Architect {
		return architectColor
	}
	if !n.HasCharter {
		return ringNone
	}
	switch maturityLevel(n.Card.Maturity) {
	case "growing", "early":
		return ringGrowing
	case "mature", "critical":
		return ringMature
	default:
		// A charter with an unrecognised maturity still counts as a charter.
		return ringMature
	}
}

func maturityLevel(value string) string {
	level, _, _ := strings.Cut(value, " - ")
	return level
}

func (c *pixelCanvas) node(n SceneNode, level string, metrics PixelMetrics, dim bool) {
	if n.Kind == "tag" {
		return
	}
	cx, cy := c.px(n.Position)
	r := nodeRadius(n, c.zoom)
	if n.Kind == "owner" || n.Kind == "cto" || n.Role == "cto" {
		col := deploysColor
		if n.Kind == "cto" || n.Role == "cto" {
			col = architectColor
		}
		if dim {
			col.A = 140
		}
		c.circle(cx, cy, r, col)
		if n.Selected {
			c.dashedCircle(cx, cy, r+4, 1, pixelAccent)
		}
		return
	}
	alpha := 1.0
	if dim {
		alpha = .55
	}
	if n.Role == "busy" {
		glowAlpha := .12 + .24*n.PulsePhase
		c.glow(cx, cy, r, 14, color.NRGBA{pixelBusy.R, pixelBusy.G, pixelBusy.B, uint8(glowAlpha * alpha * 255)})
	}
	if n.Selected {
		c.glow(cx, cy, r, 22, color.NRGBA{pixelAccent.R, pixelAccent.G, pixelAccent.B, .40 * 255})
	}
	ringWidth := 2.0
	if n.Selected {
		ringWidth = 3
	}
	ring := ringColor(n)
	if n.Selected {
		ring = pixelAccent
	}
	// Dim the node's own colours toward the background. Scaling alpha instead
	// would let the bright ring show through its darker fill.
	dimCol := func(col color.NRGBA) color.NRGBA {
		if alpha >= 1 {
			return col
		}
		col = mix(pixelBackground, col, alpha)
		col.A = 0xff
		return col
	}
	ring = dimCol(ring)
	fill := func(dx, dy float64) color.NRGBA {
		d := math.Hypot(dx+r*.25, dy+r*.25)
		t := math.Min(d/math.Max(r*1.2, .001), 1)
		return dimCol(mix(nodeFillCenter, nodeFillEdge, t))
	}
	if n.Architect {
		c.hexagonGradient(cx, cy, r+ringWidth, func(dx, dy float64) color.NRGBA { return ring })
		c.hexagonGradient(cx, cy, r, fill)
	} else {
		c.circleGradient(cx, cy, r+ringWidth, func(dx, dy float64) color.NRGBA { return ring })
		c.circleGradient(cx, cy, r, fill)
	}
	count := min(max(n.Card.Sessions, 0), 6)
	for i := 0; i < count; i++ {
		x, y := cx, cy
		if count > 1 {
			angle := -math.Pi/2 + 2*math.Pi*float64(i)/float64(count)
			x += math.Cos(angle) * r * .42
			y += math.Sin(angle) * r * .42
		}
		dot := color.NRGBA{0xd7, 0xdc, 0xe5, uint8(.55 * alpha * 255)}
		if i < n.BusySessions {
			dot = color.NRGBA{pixelBusy.R, pixelBusy.G, pixelBusy.B, uint8(alpha * 255)}
		}
		c.circle(x, y, 2.2, dot)
	}
	if n.Wildcard {
		c.dashedCircle(cx, cy, r+12, 1, color.NRGBA{pixelAccent.R, pixelAccent.G, pixelAccent.B, uint8(.7 * alpha * 255)})
	}
}

func (c *pixelCanvas) glow(cx, cy, r, extra float64, col color.NRGBA) {
	outer := r + extra
	c.circleGradient(cx, cy, outer, func(dx, dy float64) color.NRGBA {
		d := math.Hypot(dx, dy)
		t := 1 - math.Min(math.Max(d-r, 0)/extra, 1)
		t = t * t
		a := float64(col.A) / 255 * t
		return color.NRGBA{col.R, col.G, col.B, uint8(a * 255)}
	})
}

func (c *pixelCanvas) dashedCircle(cx, cy, r, width float64, col color.NRGBA) {
	circ := 2 * math.Pi * r
	for d := 0.0; d < circ; d += 1 {
		if math.Mod(d, 8) >= 3 {
			continue
		}
		a := d / r
		c.circle(cx+math.Cos(a)*r, cy+math.Sin(a)*r, width/2, col)
	}
}

func (c *pixelCanvas) talk(t SceneTalk) {
	col := talkDim
	alpha := .55
	if t.NoRule {
		col, alpha = talkBright, .9
	}
	if t.Voice {
		col, alpha = talkVoice, .95
	}
	ax, ay := c.px(t.From)
	bx, by := c.px(t.To)
	a, b := Point{ax, ay}, Point{bx, by}
	ctrl := bendControl(a, b, .07)
	spacing := 10.0
	switch {
	case t.Bucket >= 3:
		spacing = 5
	case t.Bucket == 2:
		spacing = 7
	}
	radius := 1.4
	if t.Voice {
		radius = 1.8
	}
	col.A = uint8(alpha * 255)
	next := spacing
	prev := quadPoint(a, ctrl, b, 0)
	for i := 1; i <= 64; i++ {
		u := float64(i) / 64
		p := quadPoint(a, ctrl, b, u)
		seg := math.Hypot(p.X-prev.X, p.Y-prev.Y)
		for next <= seg {
			f := next / math.Max(seg, 1e-9)
			c.circle(prev.X+(p.X-prev.X)*f, prev.Y+(p.Y-prev.Y)*f, radius, col)
			next += spacing
		}
		next -= seg
		prev = p
	}
}

func (c *pixelCanvas) edge(e SceneEdge, nodes []SceneNode) {
	col := edgeColor(e.Kind)
	width := 1.5
	alpha := 1.0
	if e.Selected {
		width, alpha = 2.2, 1
	} else if e.Dimmed {
		alpha = pixelEdgeDimmed
	}
	var points []Point
	if len(e.Path) > 1 {
		points = make([]Point, len(e.Path))
		for i, p := range e.Path {
			points[i].X, points[i].Y = c.px(p)
		}
	} else {
		ax, ay := c.px(e.From)
		bx, by := c.px(e.To)
		a, b := Point{ax, ay}, Point{bx, by}
		ctrl := bendControl(a, b, .18)
		points = sampleQuad(a, ctrl, b, 64)
	}
	if e.Kind == "deploys" {
		c.stroke(points, 4.5, col, alpha*.35, false)
	}
	c.stroke(points, width, col, alpha, e.Dashed)
	c.arrowhead(points, e, col, alpha, nodes, width)
}

func edgeColor(kind string) color.NRGBA {
	switch kind {
	case "depends":
		return dependsColor
	case "deploys":
		return deploysColor
	default:
		return ringNone
	}
}

// stroke fills a thick anti-aliased curve. The colour runs from faint at the
// source to strong at the target, along the chord between them.
func (c *pixelCanvas) stroke(points []Point, width float64, col color.NRGBA, alpha float64, dashed bool) {
	if len(points) < 2 || width <= 0 {
		return
	}
	a, b := points[0], points[len(points)-1]
	dx, dy := b.X-a.X, b.Y-a.Y
	dist := math.Max(math.Hypot(dx, dy), 1e-9)
	colourAt := func(x, y float64) color.NRGBA {
		t := math.Min(math.Max(((x-a.X)*dx+(y-a.Y)*dy)/(dist*dist), 0), 1)
		t = .35 + .65*t
		out := col
		out.A = uint8(math.Min(255, alpha*float64(out.A)*t))
		return out
	}
	runs := [][]Point{points}
	if dashed {
		runs = dashRuns(points, 6, 5)
	}
	for _, run := range runs {
		if len(run) < 2 {
			continue
		}
		bbox := strokeBBox(run, width)
		bbox = bbox.Intersect(c.img.Bounds())
		if bbox.Empty() {
			continue
		}
		path := func(z *vector.Rasterizer) { strokePath(z, run, width, bbox.Min) }
		src := gradient(bbox, func(x, y int) color.NRGBA {
			return colourAt(float64(x)+float64(bbox.Min.X)+.5, float64(y)+float64(bbox.Min.Y)+.5)
		})
		c.fill(bbox, path, src)
	}
}

func strokePath(z *vector.Rasterizer, pts []Point, width float64, origin image.Point) {
	ox, oy := float64(origin.X), float64(origin.Y)
	left, right := strokeSides(pts, width)
	z.MoveTo(float32(left[0].X-ox), float32(left[0].Y-oy))
	for _, p := range left[1:] {
		z.LineTo(float32(p.X-ox), float32(p.Y-oy))
	}
	for i := len(right) - 1; i >= 0; i-- {
		z.LineTo(float32(right[i].X-ox), float32(right[i].Y-oy))
	}
	z.ClosePath()
	// Round caps keep the ends of a dash soft.
	for _, p := range []Point{pts[0], pts[len(pts)-1]} {
		circlePath(z, p.X-ox, p.Y-oy, width/2)
	}
}

func strokeSides(pts []Point, width float64) ([]Point, []Point) {
	left := make([]Point, len(pts))
	right := make([]Point, len(pts))
	for i := range pts {
		var nx, ny float64
		if i == 0 {
			nx, ny = pts[1].Y-pts[0].Y, -(pts[1].X - pts[0].X)
		} else if i == len(pts)-1 {
			nx, ny = pts[i].Y-pts[i-1].Y, -(pts[i].X - pts[i-1].X)
		} else {
			nx, ny = pts[i+1].Y-pts[i-1].Y, -(pts[i+1].X - pts[i-1].X)
		}
		l := math.Hypot(nx, ny)
		if l == 0 {
			l = 1
		}
		nx, ny = nx/l*width/2, ny/l*width/2
		left[i] = Point{pts[i].X + nx, pts[i].Y + ny}
		right[i] = Point{pts[i].X - nx, pts[i].Y - ny}
	}
	return left, right
}

func strokeBBox(pts []Point, width float64) image.Rectangle {
	minx, miny := math.Inf(1), math.Inf(1)
	maxx, maxy := math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minx, miny = math.Min(minx, p.X), math.Min(miny, p.Y)
		maxx, maxy = math.Max(maxx, p.X), math.Max(maxy, p.Y)
	}
	m := width/2 + 2
	return image.Rect(int(math.Floor(minx-m)), int(math.Floor(miny-m)), int(math.Ceil(maxx+m)), int(math.Ceil(maxy+m)))
}

func dashRuns(pts []Point, on, off float64) [][]Point {
	period := on + off
	var runs [][]Point
	var run []Point
	dist := 0.0
	for i := 1; i < len(pts); i++ {
		seg := math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
		if seg == 0 {
			continue
		}
		steps := int(math.Ceil(seg))
		for s := 0; s < steps; s++ {
			f0, f1 := float64(s)/float64(steps), float64(s+1)/float64(steps)
			d0, d1 := dist+seg*f0, dist+seg*f1
			p0 := Point{pts[i-1].X + (pts[i].X-pts[i-1].X)*f0, pts[i-1].Y + (pts[i].Y-pts[i-1].Y)*f0}
			p1 := Point{pts[i-1].X + (pts[i].X-pts[i-1].X)*f1, pts[i-1].Y + (pts[i].Y-pts[i-1].Y)*f1}
			a0 := math.Mod(d0, period) < on
			a1 := math.Mod(d1, period) < on
			if a0 {
				if len(run) == 0 {
					run = append(run, p0)
				}
				run = append(run, p1)
			}
			if a0 != a1 || !a1 {
				if len(run) > 1 {
					runs = append(runs, run)
				}
				if !a1 {
					run = nil
				} else {
					run = []Point{p1}
				}
			}
		}
		dist += seg
	}
	if len(run) > 1 {
		runs = append(runs, run)
	}
	return runs
}

func (c *pixelCanvas) arrowhead(points []Point, e SceneEdge, col color.NRGBA, alpha float64, nodes []SceneNode, width float64) {
	end := points[len(points)-1]
	prev := points[len(points)-2]
	dx, dy := end.X-prev.X, end.Y-prev.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return
	}
	dx, dy = dx/l, dy/l
	r := 0.0
	if i := nodeIndexAt(nodes, e.To); i >= 0 {
		r = nodeRadius(nodes[i], c.zoom)
	}
	tip := Point{end.X - dx*(r+1), end.Y - dy*(r+1)}
	length := 9.0
	half := length * .52
	base := Point{tip.X - dx*length, tip.Y - dy*length}
	px, py := -dy*half, dx*half
	cx, cy := base.X+px, base.Y+py
	dx2, dy2 := base.X-px, base.Y-py
	col.A = uint8(alpha * float64(col.A))
	bbox := image.Rect(int(math.Floor(math.Min(tip.X, math.Min(cx, dx2))))-2, int(math.Floor(math.Min(tip.Y, math.Min(cy, dy2))))-2,
		int(math.Ceil(math.Max(tip.X, math.Max(cx, dx2))))+2, int(math.Ceil(math.Max(tip.Y, math.Max(cy, dy2))))+2).Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	c.solid(bbox, func(z *vector.Rasterizer) {
		ox, oy := float64(bbox.Min.X), float64(bbox.Min.Y)
		z.MoveTo(float32(tip.X-ox), float32(tip.Y-oy))
		z.LineTo(float32(cx-ox), float32(cy-oy))
		z.LineTo(float32(dx2-ox), float32(dy2-oy))
		z.ClosePath()
	}, col)
}

// nodeRadius is the radius used where an edge meets a node. The scene only
// carries the size level, so every node on screen shares it.
func bendControl(a, b Point, factor float64) Point {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return Point{(a.X + b.X) / 2, (a.Y + b.Y) / 2}
	}
	bend := math.Min(60, l*factor)
	return Point{(a.X+b.X)/2 - dy/l*bend, (a.Y+b.Y)/2 + dx/l*bend}
}

func quadPoint(a, ctrl, b Point, t float64) Point {
	u := 1 - t
	return Point{u*u*a.X + 2*u*t*ctrl.X + t*t*b.X, u*u*a.Y + 2*u*t*ctrl.Y + t*t*b.Y}
}

func sampleQuad(a, ctrl, b Point, n int) []Point {
	out := make([]Point, 0, n+1)
	for i := 0; i <= n; i++ {
		out = append(out, quadPoint(a, ctrl, b, float64(i)/float64(n)))
	}
	return out
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Min(math.Max(t, 0), 1)
	return color.NRGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: uint8(float64(a.A) + (float64(b.A)-float64(a.A))*t),
	}
}
