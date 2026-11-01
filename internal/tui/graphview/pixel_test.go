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
			{Name: "busy", Label: "busy", Position: Point{.50, .18}, Role: "busy"},
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
	img := Rasterize(pixelScene(), Size{Width: 40, Height: 16}, PixelMetrics{CellWidth: 8, CellHeight: 16})
	if img == nil {
		t.Fatal("no image")
	}
	path := filepath.Join("testdata", "pixel_scene.png")
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
