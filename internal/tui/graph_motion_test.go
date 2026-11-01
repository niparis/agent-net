package tui

import (
	"strings"
	"testing"
	"time"

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
	m.graphPixel.cellW, m.graphPixel.cellH = 8, 16
	return m
}

func TestGraphPixelStopsSendingWhenNothingMoves(t *testing.T) {
	m := idleGraphModel(120, 34)
	first := strings.Join(m.graphView(120, 34), "\n")
	if !strings.Contains(first, "\x1b_Ga=T") {
		t.Fatalf("first frame did not transmit the image:\n%q", first)
	}
	m.now = func() time.Time { return t0.Add(10 * time.Second) }
	second := strings.Join(m.graphView(120, 34), "\n")
	if strings.Contains(second, "\x1b_G") {
		t.Fatal("an unchanged idle frame was transmitted again")
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
	m.graphState.Smooth = true
	m.graphState, _ = graphview.Update(m.graphState, graphview.Tick{Now: t0})
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
	_ = m.graphView(120, 34)
	m.tab = sessionsTab
	out := m.View()
	if strings.Contains(out, "\x1b_G") || strings.Contains(out, "\U0010EEEE") {
		t.Fatal("another tab sent kitty graphics")
	}
	_, cmd := m.Update(graphTickMsg{generation: m.graphGeneration})
	if cmd != nil {
		t.Fatal("graph animation continued on another tab")
	}
}
