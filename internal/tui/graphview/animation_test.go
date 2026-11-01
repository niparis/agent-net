package graphview

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func animationGraph() *Graph {
	g := NewWithoutLayout(protocol.GraphView{
		Nodes: []protocol.Node{
			{Name: "a", Kind: "component"},
			{Name: "b", Kind: "component"},
			{Name: "c", Kind: "component"},
		},
		Edges: []protocol.Edge{{From: "a", To: "b", Kind: "depends"}},
	})
	g.Positions = map[string]Point{"a": {-2, 0}, "b": {2, 0}, "c": {2, 2}}
	return g
}

func colorNear(c color.RGBA, want color.NRGBA, tolerance int) bool {
	return absInt(int(c.R)-int(want.R)) <= tolerance &&
		absInt(int(c.G)-int(want.G)) <= tolerance &&
		absInt(int(c.B)-int(want.B)) <= tolerance
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func hasColorNear(img *image.RGBA, p Point, want color.NRGBA, radius int) bool {
	for y := int(p.Y) - radius; y <= int(p.Y)+radius; y++ {
		for x := int(p.X) - radius; x <= int(p.X)+radius; x++ {
			if image.Pt(x, y).In(img.Bounds()) && colorNear(img.RGBAAt(x, y), want, 8) {
				return true
			}
		}
	}
	return false
}

func TestCometDotFollowsTheEdgeCurve(t *testing.T) {
	g := animationGraph()
	now := time.Unix(100, 0)
	g.Message("a", "b", "request", now)
	size := Size{Width: 40, Height: 16}
	metrics := PixelMetrics{CellWidth: 8, CellHeight: 16}
	s := State{Graph: g, Camera: g.Fit(size.Width, size.Height), Now: now.Add(travelTime / 2)}
	scene := BuildScene(s, Viewport{Width: float64(size.Width), Height: float64(size.Height)})
	if len(scene.Comets) != 1 {
		t.Fatalf("comets: %+v", scene.Comets)
	}
	c := scene.Comets[0]
	if !c.HasEdge || c.Kind != "request" {
		t.Fatalf("comet lost its kind or edge: %+v", c)
	}
	w, h := float64(size.Width*metrics.CellWidth), float64(size.Height*metrics.CellHeight)
	got := cometPoint(c, w, h)
	a := Point{c.From.X * w, c.From.Y * h}
	b := Point{c.To.X * w, c.To.Y * h}
	straight := Point{a.X + (b.X-a.X)*c.Progress, a.Y + (b.Y-a.Y)*c.Progress}
	if math.Hypot(got.X-straight.X, got.Y-straight.Y) < 1 {
		t.Fatalf("dot at %v did not leave the chord %v", got, straight)
	}
	img := Rasterize(scene, size, metrics)
	if !hasColorNear(img, got, pixelAccent, 4) {
		t.Fatalf("no request-coloured dot near the curve point %v", got)
	}

	// A pair without an edge gets a straight line.
	g.Message("a", "c", "reply", now)
	s.Now = now.Add(travelTime / 2)
	scene = BuildScene(s, Viewport{Width: float64(size.Width), Height: float64(size.Height)})
	if len(scene.Comets) != 2 {
		t.Fatalf("comets: %+v", scene.Comets)
	}
	var direct SceneComet
	for _, comet := range scene.Comets {
		if comet.HasEdge {
			continue
		}
		direct = comet
	}
	if direct.Kind != "reply" {
		t.Fatalf("unexpected direct comet: %+v", direct)
	}
	got = cometPoint(direct, w, h)
	a = Point{direct.From.X * w, direct.From.Y * h}
	b = Point{direct.To.X * w, direct.To.Y * h}
	straight = Point{a.X + (b.X-a.X)*direct.Progress, a.Y + (b.Y-a.Y)*direct.Progress}
	if math.Hypot(got.X-straight.X, got.Y-straight.Y) > .5 {
		t.Fatalf("direct comet at %v is not on its line %v", got, straight)
	}
}

func TestArrivalRippleAppears(t *testing.T) {
	g := animationGraph()
	now := time.Unix(100, 0)
	g.Message("a", "b", "reply", now)
	size := Size{Width: 40, Height: 16}
	metrics := PixelMetrics{CellWidth: 8, CellHeight: 16}
	s := State{Graph: g, Camera: g.Fit(size.Width, size.Height), Now: now.Add(travelTime + flashTime/2)}
	scene := BuildScene(s, Viewport{Width: float64(size.Width), Height: float64(size.Height)})
	if len(scene.Comets) != 0 {
		t.Fatalf("arrived message is still a comet: %+v", scene.Comets)
	}
	if len(scene.Ripples) != 1 || scene.Ripples[0].Kind != "reply" {
		t.Fatalf("ripples: %+v", scene.Ripples)
	}
	withRipple := Rasterize(scene, size, metrics)
	without := scene
	without.Ripples = nil
	bare := Rasterize(without, size, metrics)
	diff := 0
	for y := 0; y < withRipple.Bounds().Dy(); y++ {
		for x := 0; x < withRipple.Bounds().Dx(); x++ {
			if withRipple.RGBAAt(x, y) != bare.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff < 20 {
		t.Fatalf("arrival ripple changed only %d pixels", diff)
	}
	w, h := float64(size.Width*metrics.CellWidth), float64(size.Height*metrics.CellHeight)
	target := Point{scene.Ripples[0].Position.X * w, scene.Ripples[0].Position.Y * h}
	radius := rippleRadius(scene.Ripples[0].Progress, metrics)
	if !hasGreenNear(withRipple, Point{target.X + radius, target.Y}, 8) {
		t.Fatalf("no reply-green ripple at %v", target)
	}
}

func hasGreenNear(img *image.RGBA, p Point, radius int) bool {
	for y := int(p.Y) - radius; y <= int(p.Y)+radius; y++ {
		for x := int(p.X) - radius; x <= int(p.X)+radius; x++ {
			if !image.Pt(x, y).In(img.Bounds()) {
				continue
			}
			c := img.RGBAAt(x, y)
			if int(c.G) > int(c.R)+6 && int(c.G) > int(c.B)-8 && int(c.G) > 40 {
				return true
			}
		}
	}
	return false
}

func TestZoomEasesOverTime(t *testing.T) {
	g := animationGraph()
	now := time.Unix(100, 0)
	fit := g.Fit(80, 20)
	s := State{Graph: g, Camera: fit, Now: now, Smooth: true}
	next, _ := Update(s, ZoomIn{})
	if next.Camera.Zoom != fit.Zoom*1.25 {
		t.Fatalf("target zoom %v", next.Camera.Zoom)
	}
	mid, _ := Update(next, Tick{Now: now.Add(150 * time.Millisecond)})
	shown := mid.ViewCamera()
	if shown.Zoom <= fit.Zoom || shown.Zoom >= next.Camera.Zoom {
		t.Fatalf("mid zoom %v is not between %v and %v", shown.Zoom, fit.Zoom, next.Camera.Zoom)
	}
	end, _ := Update(mid, Tick{Now: now.Add(300 * time.Millisecond)})
	if got := end.ViewCamera().Zoom; math.Abs(got-next.Camera.Zoom) > 1e-9 {
		t.Fatalf("final zoom %v, want %v", got, next.Camera.Zoom)
	}
	if end.Moving() {
		t.Fatal("camera still moves after the ease")
	}
}

func TestSelectionEasesCameraToTheNode(t *testing.T) {
	g := animationGraph()
	now := time.Unix(100, 0)
	s := State{Graph: g, Camera: Camera{Zoom: 1, FitZoom: 1, Center: Point{0, 0}}, Now: now, Smooth: true}
	next, _ := Update(s, SelectNode{Name: "b"})
	mid, _ := Update(next, Tick{Now: now.Add(150 * time.Millisecond)})
	if x := mid.ViewCamera().Center.X; x <= 0 || x >= 2 {
		t.Fatalf("camera centre %v did not ease toward b", mid.ViewCamera().Center)
	}
	end, _ := Update(mid, Tick{Now: now.Add(300 * time.Millisecond)})
	if end.ViewCamera().Center != g.Positions["b"] {
		t.Fatalf("camera centre %v, want %v", end.ViewCamera().Center, g.Positions["b"])
	}
}

func cardGraph() *Graph {
	g := NewWithoutLayout(protocol.GraphView{
		Nodes: []protocol.Node{
			{Name: "chartered", Kind: "component", SessionCount: 2, Charter: protocol.Charter{Maturity: "mature - broad", Purpose: "Coordinates the network and its agents with care"}},
			{Name: "plain", Kind: "component"},
		},
	})
	g.Positions = map[string]Point{"chartered": {-.2, 0}, "plain": {.2, .15}}
	return g
}

func TestCardsAppearOnlyWhenZoomedInAndChartered(t *testing.T) {
	g := cardGraph()
	size := Size{Width: 60, Height: 20}
	fit := g.Fit(size.Width, size.Height)
	fit.Center = g.Positions["chartered"]
	s := State{Graph: g, Camera: fit}
	s.Camera.Zoom = fit.FitZoom * 1.5
	scene := BuildScene(s, Viewport{Width: float64(size.Width), Height: float64(size.Height)})
	for _, n := range scene.Nodes {
		if n.ShowCard {
			t.Fatalf("%s shows a card at %vx fit", n.Name, s.Camera.Zoom/fit.FitZoom)
		}
	}
	s.Camera.Zoom = fit.FitZoom * 2
	scene = BuildScene(s, Viewport{Width: float64(size.Width), Height: float64(size.Height)})
	for _, n := range scene.Nodes {
		switch n.Name {
		case "chartered":
			if !n.ShowCard {
				t.Fatal("chartered node has no card above the threshold")
			}
		case "plain":
			if n.ShowCard {
				t.Fatal("node without a charter shows a card")
			}
		}
	}
	boxes := sceneCards(scene, size)
	if len(boxes) != 1 {
		t.Fatalf("card boxes: %+v", boxes)
	}
	lines := boxes[0].Lines
	if len(lines) < 2 || !strings.Contains(lines[0], "MATURE") || !strings.Contains(lines[0], "2") {
		t.Fatalf("card first line: %q", lines)
	}
	if strings.Count(strings.Join(lines[1:], "\n"), "\n") != 1 {
		t.Fatalf("purpose is not cut to two lines: %q", lines)
	}
	labels := PixelLabels(scene, size)
	text := labelText(labels, boxes[0].Cell)
	if !strings.Contains(text, "MATURE") || !strings.Contains(text, "Coordinates") {
		t.Fatalf("card text is not terminal text: %q", text)
	}
	withCard := Rasterize(scene, size, PixelMetrics{CellWidth: 8, CellHeight: 16})
	noCard := scene
	for i := range noCard.Nodes {
		noCard.Nodes[i].ShowCard = false
	}
	bare := Rasterize(noCard, size, PixelMetrics{CellWidth: 8, CellHeight: 16})
	diff := 0
	for y := 0; y < withCard.Bounds().Dy(); y++ {
		for x := 0; x < withCard.Bounds().Dx(); x++ {
			if withCard.RGBAAt(x, y) != bare.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff < 100 {
		t.Fatalf("card box changed only %d pixels", diff)
	}
}

func TestCardPurposeCutsToTwoLines(t *testing.T) {
	lines := wrapRunes("a very long purpose that must be cut after two lines of text", 12, 2)
	if len(lines) != 2 {
		t.Fatalf("purpose lines: %q", lines)
	}
	if !strings.HasSuffix(lines[1], "…") {
		t.Fatalf("cut purpose has no ellipsis: %q", lines)
	}
}

func labelText(labels map[Cell]LabelCell, area image.Rectangle) string {
	var b strings.Builder
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if l, ok := labels[Cell{X: x, Y: y}]; ok {
				b.WriteRune(l.Rune)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestMovingDetectsTrafficPulseAndCamera(t *testing.T) {
	g := animationGraph()
	now := time.Unix(100, 0)
	g.Message("a", "b", "request", now)
	s := State{Graph: g, Camera: Camera{Zoom: 1, FitZoom: 1}, Now: now.Add(time.Second)}
	if !s.Moving() {
		t.Fatal("message in flight is not moving")
	}
	s.Now = now.Add(travelTime + flashTime + time.Second)
	if s.Moving() {
		t.Fatal("expired traffic still moves")
	}

	busy := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "busy", Sessions: []protocol.GraphSession{{ID: "s", Status: "busy"}}}}})
	s = State{Graph: busy, Now: now}
	if !s.Moving() {
		t.Fatal("busy node pulse is not moving")
	}
	s.ReducedMotion = true
	if s.Moving() {
		t.Fatal("reduced motion still pulses")
	}

	s = State{Graph: animationGraph(), Camera: Camera{Zoom: 1, FitZoom: 1}, Now: now, Smooth: true}
	s, _ = Update(s, ZoomIn{})
	if !s.Moving() {
		t.Fatal("camera ease is not moving")
	}
	s, _ = Update(s, Tick{Now: now.Add(400 * time.Millisecond)})
	if s.Moving() {
		t.Fatal("camera ease still moves after its duration")
	}

	s = State{Graph: g, Camera: Camera{Zoom: 1, FitZoom: 1}, Now: now.Add(time.Second)}
	g.Message("a", "b", "request", now)
	s.Paused, s.PausedAt = true, now.Add(time.Second)
	if s.Moving() {
		t.Fatal("paused traffic still moves")
	}
}

func TestBusyPulseChangesTheGlow(t *testing.T) {
	size := Size{Width: 20, Height: 10}
	metrics := PixelMetrics{CellWidth: 8, CellHeight: 16}
	low := Scene{Level: "labels", Nodes: []SceneNode{{Name: "busy", Position: Point{.5, .5}, Role: "busy", PulsePhase: .1}}}
	high := Scene{Level: "labels", Nodes: []SceneNode{{Name: "busy", Position: Point{.5, .5}, Role: "busy", PulsePhase: .9}}}
	a := Rasterize(low, size, metrics)
	b := Rasterize(high, size, metrics)
	x, y := int(.5*float64(size.Width*metrics.CellWidth)), int(.5*float64(size.Height*metrics.CellHeight))
	ar, ag, ab := a.RGBAAt(x, y+18).R, a.RGBAAt(x, y+18).G, a.RGBAAt(x, y+18).B
	br, bg, bb := b.RGBAAt(x, y+18).R, b.RGBAAt(x, y+18).G, b.RGBAAt(x, y+18).B
	if int(ag)+int(ar)+int(ab) >= int(bg)+int(br)+int(bb) {
		t.Fatalf("busy glow does not pulse: low %d,%d,%d high %d,%d,%d", ar, ag, ab, br, bg, bb)
	}
}
