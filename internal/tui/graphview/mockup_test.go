package graphview

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMockupTrafficSelectionAndToggle(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}, {Name: "c"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: "depends"}}})
	g.Positions = map[string]Point{"a": {-1, 0}, "b": {0, 0}, "c": {1, 0}}
	s := State{Graph: g, Camera: Camera{Zoom: 1, FitZoom: 1}, Talk: []protocol.GraphTalkCount{{From: "a", To: "b", Count: 1}, {From: "b", To: "c", Count: 2}, {From: protocol.GraphVoiceOwner, To: "c", Voice: true}}}
	ensureVoiceOwner(g)
	viewport := Viewport{80, 20}
	g.selected = -1
	if scene := BuildScene(s, viewport); len(scene.Edges) != 1 || len(scene.Talk) != 0 {
		t.Fatalf("no selection: edges %d traffic %d", len(scene.Edges), len(scene.Talk))
	}
	g.selected = 0
	if scene := BuildScene(s, viewport); len(scene.Edges) != 1 || len(scene.Talk) != 1 {
		t.Fatalf("selected a: edges %d traffic %d", len(scene.Edges), len(scene.Talk))
	}
	s, _ = Update(s, ToggleTalk{})
	if scene := BuildScene(s, viewport); len(scene.Talk) != 3 {
		t.Fatalf("all traffic: %d", len(scene.Talk))
	}
	s, _ = Update(s, ToggleTalk{})
	if scene := BuildScene(s, viewport); len(scene.Talk) != 1 {
		t.Fatalf("selected traffic restored: %d", len(scene.Talk))
	}
}

func TestMockupMarkersStayOutsideLayout(t *testing.T) {
	view := protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: "depends"}}}
	baseline := New(view)
	view.Nodes = append(view.Nodes, protocol.Node{Name: protocol.CTONodeName, Kind: "cto"})
	withCTO := New(view)
	if baseline.Positions["a"] != withCTO.Positions["a"] || baseline.Positions["b"] != withCTO.Positions["b"] {
		t.Fatal("CTO changed project layout")
	}
	ensureVoiceOwner(withCTO)
	s := State{Graph: withCTO, Camera: withCTO.Fit(80, 20)}
	scene := BuildScene(s, Viewport{80, 20})
	var owner, cto Point
	for _, n := range scene.Nodes {
		if n.Name == protocol.GraphVoiceOwner {
			owner = n.Position
		}
		if n.Name == protocol.CTONodeName {
			cto = n.Position
		}
	}
	if owner.X > .1 || cto.X > .1 || owner == cto {
		t.Fatalf("markers not separated at left: %v %v", owner, cto)
	}
}

func TestMockupWildcardLabel(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}}, Edges: []protocol.Edge{{From: "*", To: "a", Kind: "depends", Rule: "auto"}}})
	g.Positions["a"] = Point{}
	scene := BuildScene(State{Graph: g, Camera: Camera{Zoom: 1, FitZoom: 1}}, Viewport{80, 20})
	out := (Braille{}).Render(scene, Size{80, 20})
	text := strings.Join(out.Lines, "\n")
	if !strings.Contains(text, "all projects depend · auto") || strings.Contains(text, "◂all") {
		t.Fatalf("wildcard label: %s", text)
	}
}

func TestMockupPixelBodyArrowAndGrid(t *testing.T) {
	size := Size{40, 20}
	metrics := PixelMetrics{8, 16}
	empty := Rasterize(Scene{}, size, metrics)
	grid := color.RGBAModel.Convert(empty.At(160, 160)).(color.RGBA)
	nearby := color.RGBAModel.Convert(empty.At(165, 160)).(color.RGBA)
	if grid.R <= nearby.R {
		t.Fatalf("grid point %v not brighter than background %v", grid, nearby)
	}
	node := Rasterize(Scene{Nodes: []SceneNode{{Name: "a", Position: Point{.5, .5}, Card: SceneCard{Sessions: 4}}}}, size, metrics)
	body := color.RGBAModel.Convert(node.At(171, 160)).(color.RGBA)
	back := color.RGBAModel.Convert(empty.At(171, 160)).(color.RGBA)
	if body == back {
		t.Fatalf("node body did not reach expected radius: %v", body)
	}
	canvas := &pixelCanvas{img: image.NewRGBA(image.Rect(0, 0, 320, 320)), w: 320, h: 320, zoom: 1}
	edge := SceneEdge{To: Point{.8, .5}, Kind: "depends"}
	nodes := []SceneNode{{Position: edge.To}}
	canvas.arrowhead([]Point{{210, 160}, {256, 160}}, edge, dependsColor, 1, nodes, 1.5)
	if p := canvas.img.RGBAAt(238, 157); p.G < 100 || p.B < 100 {
		t.Fatalf("arrowhead pixel: %v", p)
	}

}

func TestMockupEdgeLabelsAndNodeDetails(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "a", SessionCount: 3, Sessions: []protocol.GraphSession{{Status: "busy"}, {Status: "idle"}, {Status: "busy"}}}, {Name: "b"}, {Name: "c"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: "deploys", Mode: "owner", Rule: "auto"}, {From: "b", To: "c", Kind: "depends", Rule: "ask"}}})
	g.Positions = map[string]Point{"a": {-4, 0}, "b": {0, 0}, "c": {4, 0}}
	s := State{Graph: g, Camera: Camera{Zoom: 2, FitZoom: 2}}
	scene := BuildScene(s, Viewport{80, 20})
	if scene.Nodes[0].Card.Sessions != 3 || scene.Nodes[0].BusySessions != 2 {
		t.Fatalf("sessions: %+v", scene.Nodes[0])
	}
	text := func(scene Scene) string {
		labels := PixelLabels(scene, Size{80, 20})
		rows := make([][]rune, 20)
		for y := range rows {
			rows[y] = []rune(strings.Repeat(" ", 80))
		}
		for p, c := range labels {
			rows[p.Y][p.X] = c.Rune
		}
		var out []string
		for _, r := range rows {
			out = append(out, string(r))
		}
		return strings.Join(out, "\n")
	}
	if got := text(scene); !strings.Contains(got, "deploys · owner · auto") || strings.Contains(got, "depends · ask") {
		t.Fatalf("selected edge labels: %s", got)
	}
	s.Camera.Zoom = 2.8
	if got := text(BuildScene(s, Viewport{80, 20})); !strings.Contains(got, "depends · ask") {
		t.Fatalf("zoom edge labels: %s", got)
	}
}

func TestMockupZoomAtKeepsPointerAndDragTracksCells(t *testing.T) {
	s := State{Camera: Camera{Center: Point{2, 3}, Zoom: 4, FitZoom: 4, ScaleX: 2, ScaleY: 1.5}}
	v := Viewport{80, 20}
	x, y := 15.5, 6.5
	world := func(c Camera) Point {
		return Point{c.Center.X + (x-v.Width/2)/(2*c.Zoom*c.ScaleX), c.Center.Y + (y-v.Height/2)/(c.Zoom*c.ScaleY)}
	}
	before := world(s.Camera)
	s, _ = Update(s, ZoomAt{X: x, Y: y, Viewport: v, Factor: 1.25})
	after := world(s.Camera)
	if math.Hypot(before.X-after.X, before.Y-after.Y) > 1e-9 {
		t.Fatalf("pointer moved: %v %v", before, after)
	}
	old := s.Camera
	s, _ = Update(s, DragPan{DX: 6, DY: 3})
	px, py := s.Camera.Project(old.Center, 80, 20)
	if math.Abs(px-46) > 1e-9 || math.Abs(py-13) > 1e-9 {
		t.Fatalf("drag moved projected point to %f,%f", px, py)
	}
}

func TestMockupGridFollowsCameraAndHidesWhenSmall(t *testing.T) {
	size := Size{40, 20}
	metrics := PixelMetrics{8, 16}
	a := Rasterize(Scene{ZoomRatio: 1, GridOrigin: Point{.5, .5}}, size, metrics)
	b := Rasterize(Scene{ZoomRatio: 1, GridOrigin: Point{.5 + 5.0/320, .5}}, size, metrics)
	if a.RGBAAt(160, 160).R <= b.RGBAAt(160, 160).R {
		t.Fatal("grid did not follow the camera")
	}
	small := Rasterize(Scene{ZoomRatio: .3, GridOrigin: Point{.5, .5}}, size, metrics)
	off := Rasterize(Scene{ZoomRatio: .3, GridOrigin: Point{.5 + 5.0/320, .5}}, size, metrics)
	if !bytes.Equal(small.Pix, off.Pix) {
		t.Fatal("grid visible below ten pixels")
	}
}

func TestMockupSelectingPinnedMarkerKeepsCamera(t *testing.T) {
	g := New(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}, {Name: protocol.CTONodeName, Kind: "cto"}}})
	ensureVoiceOwner(g)
	camera := g.Fit(80, 20)
	s := State{Graph: g, Camera: camera, Smooth: true, Now: time.Unix(10, 0)}
	for _, name := range []string{protocol.CTONodeName, protocol.GraphVoiceOwner} {
		next, _ := Update(s, SelectNode{Name: name})
		if next.Graph.Selected() != name || next.Camera != camera || next.Tween != nil {
			t.Errorf("selecting %q moved camera: %+v", name, next.Camera)
		}
	}
}

func TestMockupPixelLabelsClearLargeNode(t *testing.T) {
	size := Size{80, 20}
	metrics := PixelMetrics{8, 16}
	scene := Scene{ZoomRatio: 1, Level: "labels", Nodes: []SceneNode{{Name: "project", Label: "project", Position: Point{.5, .5}, Card: SceneCard{Sessions: 6}}}}
	labels := PixelLabels(scene, size, metrics)
	for cell, l := range labels {
		if l.Rune == 'p' && cell.Y == 10 && float64(cell.X*metrics.CellWidth) < 320+nodeRadius(scene.Nodes[0], 1) {
			t.Fatalf("label overlaps large node at %+v", cell)
		}
	}
}
