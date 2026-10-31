package graphview

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestBrailleGraphShowsNodesAndWildcard(t *testing.T) {
	g := New(fixture())
	out := strings.Join((Braille{}).Render(g, g.Fit(60, 15), 60, 15), "\n")
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
	b := Braille{Now: time.Unix(11, 0), Color: func(role, text string) string { roles[role] = true; return text }}
	out := strings.Join(b.Render(g, Camera{Zoom: 2, FitZoom: 2}, 60, 20), "\n")
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
	b.Now = time.Unix(11, 500000000)
	roles = map[string]bool{}
	b.Render(g, Camera{Zoom: 2, FitZoom: 2}, 60, 20)
	if !roles["busyPulse"] {
		t.Error("missing busy pulse")
	}
	cards := strings.Join(b.Render(g, Camera{Zoom: 4, FitZoom: 2}, 60, 20), "\n")
	if !strings.Contains(cards, "[busy") {
		t.Errorf("missing card: %s", cards)
	}
}
