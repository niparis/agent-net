package graphview

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"
)

// cometColor is the traffic colour: requests in the accent, replies in green,
// deploys in amber, and the owner's words in gold.
func cometColor(c SceneComet) color.NRGBA {
	if c.Voice {
		return talkVoice
	}
	switch c.Kind {
	case "reply":
		return ringGrowing
	case "deploy", "deploys":
		return deploysColor
	default:
		return pixelAccent
	}
}

// cometPoint places a comet on its edge curve, or on the straight line when
// the two nodes have no edge. Coordinates are pixels.
func cometPoint(c SceneComet, w, h float64) Point {
	a := Point{c.From.X * w, c.From.Y * h}
	b := Point{c.To.X * w, c.To.Y * h}
	if c.HasEdge {
		return quadPoint(a, bendControl(a, b, .11), b, c.Progress)
	}
	return Point{a.X + (b.X-a.X)*c.Progress, a.Y + (b.Y-a.Y)*c.Progress}
}

// comet draws a small glowing head with a short fading tail.
func (c *pixelCanvas) comet(comet SceneComet) {
	col := cometColor(comet)
	head := cometPoint(comet, c.w, c.h)
	c.glow(head.X, head.Y, 2.5, 9, color.NRGBA{col.R, col.G, col.B, 140})
	for i := 0; i < 6; i++ {
		back := comet
		back.Progress = comet.Progress - float64(i+1)*.018
		if back.Progress < 0 {
			break
		}
		p := cometPoint(back, c.w, c.h)
		alpha := .55 * (1 - float64(i)/6)
		tail := color.NRGBA{col.R, col.G, col.B, uint8(alpha * 255)}
		radius := 2.6 - float64(i)*.3
		c.circle(p.X, p.Y, radius, tail)
	}
	c.circle(head.X, head.Y, 3.4, col)
	c.circle(head.X, head.Y, 1.4, color.NRGBA{0xff, 0xff, 0xff, 0xdd})
}

// rippleRadius is the size of the arrival ring in pixels.
func rippleRadius(progress float64, metrics PixelMetrics) float64 {
	base := math.Min(float64(metrics.CellWidth), float64(metrics.CellHeight))
	if base < 4 {
		base = 4
	}
	return base*.55 + math.Min(math.Max(progress, 0), 1)*base*1.1
}

// ripple draws the ring that plays when a message reaches its target.
func (c *pixelCanvas) ripple(r SceneRipple) {
	comet := SceneComet{Kind: r.Kind, Voice: r.Voice}
	col := cometColor(comet)
	alpha := (1 - math.Min(math.Max(r.Progress, 0), 1)) * .8
	col.A = uint8(alpha * 255)
	x, y := c.px(r.Position)
	c.annulus(x, y, rippleRadius(r.Progress, c.metrics), 1.6, col)
}

// annulus fills a ring between r and r-width. The two circles wind in opposite
// directions, so their interiors cancel.
func (c *pixelCanvas) annulus(cx, cy, r, width float64, col color.NRGBA) {
	inner := r - width
	if r <= 0 || inner <= 0 {
		return
	}
	bbox := image.Rect(int(math.Floor(cx-r))-1, int(math.Floor(cy-r))-1, int(math.Ceil(cx+r))+1, int(math.Ceil(cy+r))+1).Intersect(c.img.Bounds())
	if bbox.Empty() {
		return
	}
	c.solid(bbox, func(z *vector.Rasterizer) {
		ox, oy := float64(bbox.Min.X), float64(bbox.Min.Y)
		circlePath(z, cx-ox, cy-oy, r)
		circlePathReverse(z, cx-ox, cy-oy, inner)
	}, col)
}
