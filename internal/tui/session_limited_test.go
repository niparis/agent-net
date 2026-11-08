package tui

import (
	"strings"
	"testing"
	"time"
)

// A session under a provider usage limit shows "limited" in STATUS and starts
// its TASK with the time the limit ends.
func TestSessionLimitedScene(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := newModel(size.w, size.h)
		m.activeOnly = false
		a := &m.snap.Agents[0]
		a.Status = "idle"
		a.Profile.Task = "ingest the forge batch"
		a.LimitedUntil = t0.Add(102 * time.Minute).Unix()
		a.LimitedReason = "ChatGPT usage limit"
		m.selected = a.SessionID
		if got := m.sessionStatusCell(*a); got != "limited" {
			t.Fatalf("status: %q", got)
		}
		if got := m.statusCell(*a); got != "limited" {
			t.Fatalf("needs status: %q", got)
		}
		view := m.testView()
		if !strings.Contains(view, "limited") || !strings.Contains(view, "until 15:47") {
			t.Fatalf("%dx%d view lacks the limit:\n%s", size.w, size.h, view)
		}
		golden(t, "sessions_limited_"+itoa(size.w)+"x"+itoa(size.h), view)
	}
}

// The limit is over once its time passes, with no new report from the daemon.
func TestSessionLimitedEnds(t *testing.T) {
	m := newModel(120, 34)
	a := &m.snap.Agents[0]
	a.Status = "idle"
	a.LimitedUntil = t0.Add(-time.Minute).Unix()
	if got := m.sessionStatusCell(*a); got == "limited" {
		t.Fatal("a past limit still shows")
	}
}
