package tui

import (
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

func idleGraphModel(w, h int) Model {
	view := protocol.GraphView{
		Nodes: []protocol.Node{{Name: "alpha", Kind: "component"}, {Name: "beta", Kind: "component"}},
		Edges: []protocol.Edge{{From: "alpha", To: "beta", Kind: "depends"}},
	}
	m := mapModel(w, h, view)
	m.applyMap(mapMsg{view: view, at: t0})
	m.tab = graphTab
	gw, gh := m.graphSize()
	m.graphState.Camera = m.graphState.Graph.Fit(gw, gh)
	m.graphPixel.graphics = true
	m.graphPixel.profile = colorprofile.TrueColor
	m.graphPixel.cellW, m.graphPixel.cellH = 8, 16
	return m
}

func TestGraphPixelStopsSendingWhenNothingMoves(t *testing.T) {
	m := idleGraphModel(120, 34)
	m = renderGraphFrame(t, m)
	first := m.testView()
	if !strings.Contains(first, "\x1b_Ga=T") {
		t.Fatalf("first frame did not transmit the image:\n%q", first)
	}
	m.now = func() time.Time { return t0.Add(10 * time.Second) }
	m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: m.now()})
	if cmd := m.scheduleGraphRender(); cmd != nil {
		t.Fatal("an idle scene scheduled another frame")
	}
	second := m.testView()
	if second != first {
		t.Fatal("an unchanged idle frame changed, so the renderer would send it again")
	}
	if !strings.Contains(second, "\U0010EEEE") {
		t.Fatal("idle frame lost its placeholders")
	}
}

func TestGraphFrameIntervalFollowsMotion(t *testing.T) {
	if graphFrameInterval >= graphIdleInterval {
		t.Fatalf("frame interval %v must be shorter than idle %v", graphFrameInterval, graphIdleInterval)
	}
	if graphFrameInterval < time.Second/15 {
		t.Fatalf("frame interval %v is faster than 15 frames a second", graphFrameInterval)
	}
	m := graphModel(120, 34)
	m.graphPixel.graphics = true
	// Without the motion switch the pixel graph stays still and never asks
	// for animation frames.
	m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: t0})
	if got := m.graphFrameInterval(); got != graphIdleInterval {
		t.Fatalf("still graph interval %v, want %v", got, graphIdleInterval)
	}
	m.graphPixel.motion = true
	m.graphState.Smooth = true
	if got := m.graphFrameInterval(); got != graphFrameInterval {
		t.Fatalf("traffic interval %v, want %v", got, graphFrameInterval)
	}

	// A busy node keeps a slow pulse alive, so it also needs frames.
	m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: t0.Add(10 * time.Second)})
	if got := m.graphFrameInterval(); got != graphFrameInterval {
		t.Fatalf("busy pulse interval %v, want %v", got, graphFrameInterval)
	}

	// With reduced motion and no traffic, frames stop.
	m.graphState.ReducedMotion = true
	if got := m.graphFrameInterval(); got != graphIdleInterval {
		t.Fatalf("idle interval %v, want %v", got, graphIdleInterval)
	}

	// Pause freezes the traffic and the pulse.
	m.graphState.ReducedMotion = false
	m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: t0})
	m.graphState, _ = graphview.Update(m.graphState, graphview.TogglePause{})
	if got := m.graphFrameInterval(); got != graphIdleInterval {
		t.Fatalf("paused interval %v, want %v", got, graphIdleInterval)
	}

	// Braille keeps its existing tick.
	m.graphPixel.graphics = false
	m.graphState.Paused = false
	if got := m.graphFrameInterval(); got != graphIdleInterval {
		t.Fatalf("braille interval %v, want %v", got, graphIdleInterval)
	}
}

func TestGraphPixelSendsNothingOnAnotherTab(t *testing.T) {
	m := idleGraphModel(120, 34)
	m = renderGraphFrame(t, m)
	m.tab = sessionsTab
	out := m.testView()
	if strings.Contains(out, "\x1b_G") || strings.Contains(out, "\U0010EEEE") {
		t.Fatal("another tab sent kitty graphics")
	}
	_, cmd := m.Update(graphTickMsg{generation: m.graphGeneration})
	if cmd != nil {
		t.Fatal("graph animation continued on another tab")
	}
	m.tab = graphTab
	if back := m.testView(); !strings.Contains(back, "\x1b_Ga=T") {
		t.Fatal("the finished frame was not sent when the Graph tab returned")
	}
}

func TestGraphRenderDropsFramesWhileOneIsInFlight(t *testing.T) {
	original := rasterizeFrame
	defer func() { rasterizeFrame = original }()
	release := make(chan struct{})
	rasterizeFrame = func(graphview.Scene, graphview.Size, graphview.PixelMetrics) *image.RGBA {
		<-release
		return image.NewRGBA(image.Rect(0, 0, 4, 4))
	}
	m := idleGraphModel(120, 34)
	first := m.scheduleGraphRender()
	if first == nil {
		t.Fatal("first frame was not scheduled")
	}

	// The selection moves while the first frame is still being drawn.
	m.graphState, _ = graphview.Update(m.graphState, graphview.SelectNode{Name: "beta"})
	if next := m.scheduleGraphRender(); next != nil {
		t.Fatal("a second frame started while one was in flight")
	}

	close(release)
	frame, ok := first().(graphFrameMsg)
	if !ok {
		t.Fatal("render command returned no frame")
	}
	next, _ := m.Update(frame)
	m = next.(Model)
	if !m.graphPixel.rendering {
		t.Fatal("the newer scene was not rendered after the first frame finished")
	}
}

func TestGraphViewAndUpdateNeverRasterize(t *testing.T) {
	original := rasterizeFrame
	defer func() { rasterizeFrame = original }()
	called := make(chan struct{}, 1)
	rasterizeFrame = func(graphview.Scene, graphview.Size, graphview.PixelMetrics) *image.RGBA {
		select {
		case called <- struct{}{}:
		default:
		}
		time.Sleep(2 * time.Second)
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	m := idleGraphModel(200, 45)
	m.graphPixel.cellW, m.graphPixel.cellH = 18, 38

	start := time.Now()
	out := m.testView()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("View took %v with a 200x45 scene", elapsed)
	}
	if !strings.ContainsFunc(out, func(r rune) bool { return r >= 0x2800 && r <= 0x28ff }) {
		t.Fatal("View did not fall back to Braille before the first frame")
	}

	start = time.Now()
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 45})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Update took %v with a 200x45 scene", elapsed)
	}
	select {
	case <-called:
		t.Fatal("View or Update called the rasterizer")
	default:
	}
}
