package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
)

func TestStatusLineShowsGatewayExposureWarning(t *testing.T) {
	// Without warning
	m := newModel(120, 24)
	m.haveData = true
	m.snap = operator.Snapshot{}
	line := ansiStrip(m.titleLine(120))
	if strings.Contains(line, "gateway reachable on public address") {
		t.Fatalf("expected no exposure warning, got: %s", line)
	}

	// With warning
	m.snap.GatewayWarnings = []string{"gateway reachable on public address 203.0.113.1"}
	lineWithWarning := ansiStrip(m.titleLine(120))
	if !strings.Contains(lineWithWarning, "gateway reachable on public address 203.0.113.1") {
		t.Fatalf("expected exposure warning in status line, got: %s", lineWithWarning)
	}
}

func ansiStrip(s string) string {
	return stripStyles(s)
}
