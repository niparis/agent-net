package tui

import (
	"strings"
	"testing"
	"time"
)

func TestSessionStallScene(t *testing.T) {
	m := newModel(120, 34)
	a := &m.snap.Agents[0]
	a.Status = "busy"
	a.StalledSince = t0.Add(-12 * time.Minute).Unix()
	m.selected = a.SessionID
	if got := m.statusCell(*a); got != "stalled 12m" {
		t.Fatalf("status: %q", got)
	}
	detail := strings.Join(m.detailLines(120, 20), "\n")
	if !strings.Contains(detail, "harness activity") || !strings.Contains(detail, "Enter") || !strings.Contains(detail, "restart") {
		t.Fatalf("detail: %s", detail)
	}
	golden(t, "sessions_stalled_120x34", m.testView())
}

func TestSessionStallLostRowVisible(t *testing.T) {
	m := newModel(120, 34)
	a := &m.snap.Agents[0]
	a.Online = false
	a.StalledSince = t0.Add(-12 * time.Minute).Unix()
	id := a.SessionID
	if got := m.statusCell(*a); got != "stalled 12m" {
		t.Fatalf("lost status: %q", got)
	}
	found := false
	for _, g := range m.groups() {
		for _, row := range g.agents {
			if row.SessionID == id {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("lost stalled session hidden")
	}
}
