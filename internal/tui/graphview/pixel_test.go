package graphview

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

var updatePixel = flag.Bool("update", false, "rewrite the pixel golden images")

func rgba(r, g, b, a uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: a} }

func pixelScene() Scene {
	return Scene{
		Level: "labels",
		Nodes: []SceneNode{
			{Name: "core", Label: "core", Position: Point{.50, .50}, Selected: true, HasCharter: true, Card: SceneCard{Maturity: "mature"}},
			{Name: "blue", Label: "blue", Position: Point{.20, .28}, HasCharter: true, Card: SceneCard{Maturity: "mature"}},
			{Name: "green", Label: "green", Position: Point{.82, .26}, HasCharter: true, Card: SceneCard{Maturity: "growing"}},
			{Name: "grey", Label: "grey", Position: Point{.22, .80}, Wildcard: true},
			{Name: "hex", Label: "hex", Position: Point{.80, .78}, Architect: true, HasCharter: true, Card: SceneCard{Maturity: "mature"}},
			{Name: "busy", Label: "busy", Position: Point{.50, .18}, Role: "busy", PulsePhase: .5},
		},
		Edges: []SceneEdge{
			{From: Point{.20, .28}, To: Point{.50, .50}, Kind: "depends", Selected: true},
			{From: Point{.80, .78}, To: Point{.20, .28}, Kind: "governs", Dashed: true},
			{From: Point{.82, .26}, To: Point{.50, .50}, Kind: "deploys"},
			{From: Point{.22, .80}, To: Point{.82, .26}, Kind: "depends", Dimmed: true},
		},
		Talk: []SceneTalk{
			{From: Point{.50, .50}, To: Point{.22, .80}, Bucket: 2, NoRule: true},
			{From: Point{.82, .26}, To: Point{.50, .18}, Bucket: 1},
		},
	}
}

func TestRasterizeGolden(t *testing.T) {
	checkPixelGolden(t, pixelScene(), "pixel_scene.png")
}

func checkPixelGolden(t *testing.T, scene Scene, name string) {
	t.Helper()
	img := Rasterize(scene, Size{Width: 40, Height: 16}, PixelMetrics{CellWidth: 8, CellHeight: 16})
	if img == nil {
		t.Fatal("no image")
	}
	path := filepath.Join("testdata", name)
	if *updatePixel {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if want.Bounds() != img.Bounds() {
		t.Fatalf("golden bounds %v, got %v", want.Bounds(), img.Bounds())
	}
	diff := 0
	var first image.Point
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if want.At(x, y) != img.At(x, y) {
				if diff == 0 {
					first = image.Pt(x, y)
				}
				diff++
			}
		}
	}
	if diff != 0 {
		t.Fatalf("%d pixels differ from %s, first at %v", diff, path, first)
	}
}

// trafficScene is a frame in the middle of traffic: a request rides an edge, a
// reply takes a straight line, and a deploy has just arrived.
func trafficScene() Scene {
	return Scene{
		Level: "labels",
		Nodes: []SceneNode{
			{Name: "left", Label: "left", Position: Point{.14, .52}, HasCharter: true, Selected: true, Card: SceneCard{Maturity: "mature"}},
			{Name: "right", Label: "right", Position: Point{.84, .48}, HasCharter: true, Card: SceneCard{Maturity: "growing"}},
			{Name: "busy", Label: "busy", Position: Point{.48, .16}, Role: "busy", PulsePhase: .9},
		},
		Edges: []SceneEdge{
			{From: Point{.14, .52}, To: Point{.84, .48}, Kind: "depends", Selected: true},
			{From: Point{.48, .16}, To: Point{.84, .48}, Kind: "depends"},
		},
		Comets: []SceneComet{
			{Position: Point{.55, .5}, Kind: "request", From: Point{.14, .52}, To: Point{.84, .48}, Progress: .62, HasEdge: true},
			{Position: Point{.58, .34}, Kind: "reply", From: Point{.84, .48}, To: Point{.48, .16}, Progress: .55},
		},
		Ripples: []SceneRipple{{Position: Point{.84, .48}, Kind: "deploy", Progress: .45}},
	}
}

// cardsScene is a zoomed-in frame: the chartered node shows a card, the plain
// node does not.
func cardsScene() Scene {
	return Scene{
		Level: "cards",
		Nodes: []SceneNode{
			{Name: "core", Label: "core", Position: Point{.25, .5}, HasCharter: true, ShowCard: true, Selected: true,
				Card: SceneCard{Name: "core", Maturity: "mature - broad", Purpose: "Coordinates the network and its agents", Sessions: 2}},
			{Name: "plain", Label: "plain", Position: Point{.72, .72}},
		},
	}
}

func TestRasterizeMotionGoldens(t *testing.T) {
	checkPixelGolden(t, trafficScene(), "pixel_traffic.png")
	checkPixelGolden(t, cardsScene(), "pixel_cards.png")
}

// benchScene is a typical full frame: 20 nodes, edges, a busy node, a outgoing
// message and one charter card at label level.
func benchScene() Scene {
	scene := Scene{Level: "labels"}
	for i := 0; i < 20; i++ {
		p := Point{X: .1 + float64(i%5)*.2, Y: .12 + float64(i/5)*.25}
		scene.Nodes = append(scene.Nodes, SceneNode{
			Name:       itoa(i),
			Label:      "node-" + itoa(i),
			Position:   p,
			HasCharter: i%3 == 0,
			Role:       map[bool]string{true: "busy", false: "idle"}[i == 7],
			PulsePhase: .6,
			Card:       SceneCard{Maturity: "mature", Purpose: "A sample project", Sessions: 2},
		})
	}
	for i := 0; i < 12; i++ {
		scene.Edges = append(scene.Edges, SceneEdge{From: scene.Nodes[i].Position, To: scene.Nodes[(i+3)%20].Position, Kind: []string{"depends", "governs", "deploys"}[i%3]})
	}
	scene.Comets = []SceneComet{{From: scene.Nodes[1].Position, To: scene.Nodes[9].Position, Position: scene.Nodes[1].Position, Progress: .5, Kind: "request", HasEdge: true}}
	scene.Ripples = []SceneRipple{{Position: scene.Nodes[4].Position, Kind: "reply", Progress: .4}}
	return scene
}

func BenchmarkRasterizeFrame120x34(b *testing.B) {
	scene := benchScene()
	size := Size{Width: 120, Height: 34}
	metrics := PixelMetrics{CellWidth: 8, CellHeight: 16}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if img := Rasterize(scene, size, metrics); img == nil {
			b.Fatal("no image")
		}
	}
}

func TestSelectedRingIsAccent(t *testing.T) {
	scene := Scene{Level: "labels", Nodes: []SceneNode{{Name: "solo", Label: "solo", Position: Point{.5, .5}, Selected: true}}}
	img := Rasterize(scene, Size{Width: 20, Height: 10}, PixelMetrics{CellWidth: 8, CellHeight: 16})
	accent := rgba(0x5f, 0xc4, 0xc9, 0xff)
	count := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.RGBAAt(x, y) == accent {
				count++
			}
		}
	}
	// A 3 px ring around a radius near 7 px is over 90 pixels.
	if count < 60 {
		t.Fatalf("only %d accent pixels, want a visible selection ring", count)
	}
	plain := Rasterize(Scene{Level: "labels", Nodes: []SceneNode{{Name: "solo", Position: Point{.5, .5}}}}, Size{Width: 20, Height: 10}, PixelMetrics{CellWidth: 8, CellHeight: 16})
	count = 0
	for y := 0; y < plain.Bounds().Dy(); y++ {
		for x := 0; x < plain.Bounds().Dx(); x++ {
			if plain.RGBAAt(x, y) == accent {
				count++
			}
		}
	}
	if count >= 100 {
		t.Fatalf("unselected node has %d accent pixels", count)
	}
}

func TestDimmedNodeIsDimmer(t *testing.T) {
	size := Size{Width: 20, Height: 10}
	metrics := PixelMetrics{CellWidth: 8, CellHeight: 16}
	brightScene := Scene{Level: "labels", Nodes: []SceneNode{{Name: "focus", Position: Point{.80, .80}}, {Name: "away", Position: Point{.20, .30}}}}
	dimScene := Scene{Level: "labels", Nodes: []SceneNode{{Name: "focus", Position: Point{.80, .80}, Selected: true}, {Name: "away", Position: Point{.20, .30}}}}
	bright := Rasterize(brightScene, size, metrics)
	dim := Rasterize(dimScene, size, metrics)
	x, y := int(.20*float64(size.Width*metrics.CellWidth)), int(.30*float64(size.Height*metrics.CellHeight))
	lum := func(img *image.RGBA) int {
		c := img.RGBAAt(x, y)
		return int(c.R) + int(c.G) + int(c.B)
	}
	// With a selection elsewhere, a node with no edge to it must be dimmed.
	if lum(dim) >= lum(bright) {
		t.Fatalf("dimmed node luminance %d, not below %d", lum(dim), lum(bright))
	}
	// And the selected node keeps a bright fill.
	sx, sy := int(.80*float64(size.Width*metrics.CellWidth)), int(.80*float64(size.Height*metrics.CellHeight))
	if c := dim.RGBAAt(sx, sy); int(c.R)+int(c.G)+int(c.B) <= lum(dim) {
		t.Fatal("selected node is not brighter than the dimmed one")
	}
}

func TestHashImageDetectsChange(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 4, 4))
	b := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if HashImage(a) != HashImage(b) {
		t.Fatal("equal images hash differently")
	}
	b.SetRGBA(1, 1, rgba(1, 2, 3, 255))
	if HashImage(a) == HashImage(b) {
		t.Fatal("changed image keeps its hash")
	}
}
