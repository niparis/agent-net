package graphview

import (
	"github.com/niparis/agent-net/internal/protocol"
	"math/bits"
	"strings"
	"testing"
	"time"
)

func renderGraph(b Braille, g *Graph, cam Camera, width, height int, now time.Time) []string {
	scene := BuildScene(State{Graph: g, Camera: cam, Now: now}, Viewport{Width: float64(width), Height: float64(height)})
	return b.Render(scene, Size{Width: width, Height: height}).Lines
}

func TestBrailleGraphShowsNodesAndWildcard(t *testing.T) {
	g := New(fixture())
	out := strings.Join(renderGraph(Braille{}, g, g.Fit(60, 15), 60, 15, time.Time{}), "\n")
	for _, want := range []string{"a", "b", "c", "◂all"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "⠁⠂⠄⡀⢀⠈⠐⠠⠃⠆⠤⢄⣀⠉⠒⠚⠶⣶⣿") {
		t.Fatalf("no braille line:\n%s", out)
	}
}

func TestBrailleFeatures(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "busy", Sessions: []protocol.GraphSession{{Status: "busy"}}}, {Name: "idle", SessionCount: 1}, {Name: "arch", Kind: "architect"}, {Name: "empty"}}, Edges: []protocol.Edge{{From: "busy", To: "idle", Kind: "depends"}, {From: "arch", To: "empty", Kind: "governs"}, {From: "*", To: "empty", Kind: "deploys"}}})
	g.Positions = map[string]Point{"busy": {-3, -2}, "idle": {3, -2}, "arch": {-3, 2}, "empty": {3, 2}}
	g.Message("busy", "idle", "request", time.Unix(10, 0))
	roles := map[string]bool{}
	b := Braille{Color: func(role, text string) string { roles[role] = true; return text }}
	now := time.Unix(11, 0)
	out := strings.Join(renderGraph(b, g, Camera{Zoom: 2, FitZoom: 2}, 60, 20, now), "\n")
	for _, want := range []string{"●", "○", "◆", "◂all", "→"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	for _, role := range []string{"depends", "governs", "busy", "comet", "trail"} {
		if !roles[role] {
			t.Errorf("missing role %s", role)
		}
	}
	now = time.Unix(11, 500000000)
	roles = map[string]bool{}
	renderGraph(b, g, Camera{Zoom: 2, FitZoom: 2}, 60, 20, now)
	if !roles["busyPulse"] {
		t.Error("missing busy pulse")
	}
	cards := strings.Join(renderGraph(b, g, Camera{Zoom: 4, FitZoom: 2}, 60, 20, now), "\n")
	if !strings.Contains(cards, "[busy") {
		t.Errorf("missing card: %s", cards)
	}
}

func TestBrailleSelectionAndFlash(t *testing.T) {
	g := New(fixture())
	g.Message("a", "b", "request", time.Unix(10, 0))
	roles := map[string]bool{}
	b := Braille{Color: func(role, text string) string { roles[role] = true; return text }}
	out := strings.Join(renderGraph(b, g, g.Fit(80, 20), 80, 20, time.Unix(13, 100000000)), "\n")
	if !strings.Contains(out, "▸") || !roles["selected"] {
		t.Fatal("selection not visible")
	}
	if !roles["flash"] {
		t.Fatal("arrival flash not visible")
	}
}

func TestBrailleDashAndEdgeRoles(t *testing.T) {
	makeGraph := func(kind string) *Graph {
		g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "a"}, {Name: "b"}}, Edges: []protocol.Edge{{From: "a", To: "b", Kind: kind}}})
		g.Positions = map[string]Point{"a": {-4, 0}, "b": {4, 0}}
		return g
	}
	dots := func(kind string) int {
		n := 0
		roleSeen := false
		b := Braille{Color: func(role, text string) string {
			if role == kind {
				roleSeen = true
			}
			for _, r := range text {
				if r >= 0x2800 && r <= 0x28ff {
					n += bits.OnesCount8(uint8(r - 0x2800))
				}
			}
			return text
		}}
		renderGraph(b, makeGraph(kind), Camera{Zoom: 3, FitZoom: 3}, 70, 10, time.Time{})
		if !roleSeen {
			t.Errorf("missing edge role %s", kind)
		}
		return n
	}
	solid := dots("depends")
	dashed := dots("governs")
	dots("deploys")
	if dashed >= solid {
		t.Fatalf("dash has %d dots, solid %d", dashed, solid)
	}
}
func TestBrailleShortensLabel(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "a-very-long-project-name"}}})
	g.Positions["a-very-long-project-name"] = Point{}
	out := strings.Join(renderGraph(Braille{}, g, Camera{Zoom: 1, FitZoom: 1}, 10, 3, time.Time{}), "\n")
	if !strings.Contains(out, "…") {
		t.Fatalf("label was not shortened: %s", out)
	}
}

func TestBrailleCardIncludesDetails(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "alpha", SessionCount: 2, Charter: protocol.Charter{Purpose: "handles requests"}}}})
	g.Positions["alpha"] = Point{}
	out := strings.Join(renderGraph(Braille{}, g, Camera{Zoom: 4, FitZoom: 1}, 70, 15, time.Time{}), "\n")
	if !strings.Contains(out, "2 sessions") || !strings.Contains(out, "handles requests") {
		t.Fatalf("missing card details:\n%s", out)
	}
}

func TestBrailleLabelsSitOneSpaceFromTheirMarkers(t *testing.T) {
	g := NewWithoutLayout(protocol.GraphView{Nodes: []protocol.Node{{Name: "alpha"}, {Name: "beta"}}})
	g.Positions = map[string]Point{"alpha": {-2, 0}, "beta": {2, 0}}
	out := strings.Join(renderGraph(Braille{}, g, Camera{Zoom: 2, FitZoom: 2}, 50, 9, time.Time{}), "\n")
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(out, "○ "+name) && !strings.Contains(out, name+" ○") {
			t.Errorf("%s is not adjacent to its marker:\n%s", name, out)
		}
	}
}

func TestTalkDensityAndEdgePriority(t *testing.T) {
	size := Size{Width: 60, Height: 8}
	count := func(bucket int) int {
		out := (Braille{}).Render(Scene{Talk: []SceneTalk{{From: Point{.1, .5}, To: Point{.9, .5}, Bucket: bucket, NoRule: true}}}, size)
		n := 0
		for _, r := range out.Lines[4] {
			if r != ' ' {
				n++
			}
		}
		return n
	}
	if count(1) >= count(3) {
		t.Fatalf("higher bucket should have denser visible dots: %d %d", count(1), count(3))
	}
	roles := map[string]bool{}
	scene := Scene{Talk: []SceneTalk{{From: Point{.1, .5}, To: Point{.9, .5}, Bucket: 3}}, Edges: []SceneEdge{{From: Point{.1, .5}, To: Point{.9, .5}, Kind: "depends"}}}
	(Braille{Color: func(role, text string) string { roles[role] = true; return text }}).Render(scene, size)
	if !roles["depends"] || roles["talkFaint"] {
		t.Fatalf("edge should cover talk: %v", roles)
	}
}

func TestHitMapIncludesLabelCells(t *testing.T) {
	scene := Scene{Nodes: []SceneNode{{Name: "alpha", Label: "alpha", Position: Point{.5, .5}}}}
	out := (Braille{}).Render(scene, Size{Width: 40, Height: 10})
	for y, line := range out.Lines {
		for x, r := range []rune(line) {
			if r == 'a' {
				if got := out.Hits[Cell{X: x, Y: y}]; got != "alpha" {
					t.Fatalf("label hit %q", got)
				}
				return
			}
		}
	}
	t.Fatal("label missing")
}
