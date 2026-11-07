package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestQuotaWorkerFloorsInlineEntryAndPause(t *testing.T) {
	m := newModel(80, 24)
	m.tab = quotasTab
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	m.usage.raw.Local.Quotas = &protocol.Quotas{
		GeneratedAt: t0.Unix(),
		Providers: []protocol.QuotaProvider{
			{
				Provider: "claude",
				Plan:     "max",
				Windows:  []protocol.QuotaWindow{{ID: "5h", PercentUsed: 70}}, // 30% left
			},
		},
	}

	// Initially floor is off (0) -> title shows hint, no FLOOR column, not paused
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "f set worker floor") {
		t.Fatalf("expected 'f set worker floor' in title line:\n%s", view)
	}
	if strings.Contains(view, "FLOOR") {
		t.Fatalf("did not expect FLOOR column when no floor configured:\n%s", view)
	}
	if strings.Contains(view, "under floor") {
		t.Fatalf("should not be paused when floor is off:\n%s", view)
	}

	// Press 'f' -> opens modeQuotaFloor inline entry
	m = keys(m, "f")
	if m.mode != modeQuotaFloor {
		t.Fatalf("expected modeQuotaFloor, got %v", m.mode)
	}
	view = ansi.Strip(m.testView())
	if !strings.Contains(view, "floor % for claude") {
		t.Fatalf("expected prompt in view:\n%s", view)
	}
	if !strings.Contains(view, "workers pause when quota falls below this floor") {
		t.Fatalf("expected floor explanation banner in view:\n%s", view)
	}

	// Escape cancels without changing floor
	m = keys(m, "esc")
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after esc, got %v", m.mode)
	}
	if m.snap.WorkerFloors.Floor("anthropic") != 0 {
		t.Fatalf("floor should remain 0 after esc, got %d", m.snap.WorkerFloors.Floor("anthropic"))
	}

	// Press 'f', enter invalid floor 99 -> error
	m = keys(m, "f", "9", "9", "enter")
	if m.mode != modeQuotaFloor {
		t.Fatalf("expected to remain in modeQuotaFloor on invalid input, got %v", m.mode)
	}

	// Press esc, then 'f', type '4', '0', enter -> floor sets to 40%
	m = keys(m, "esc")
	m = keys(m, "f", "4", "0", "enter")
	if m.snap.WorkerFloors.Floor("anthropic") != 40 {
		t.Fatalf("expected floor 40, got %d", m.snap.WorkerFloors.Floor("anthropic"))
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after submit, got %v", m.mode)
	}

	// Now anyFloor is true: FLOOR column shown, 30% left < 40% floor -> marked "under floor"
	view = ansi.Strip(m.testView())
	if !strings.Contains(view, "FLOOR") {
		t.Fatalf("expected FLOOR column in header:\n%s", view)
	}
	if !strings.Contains(view, "40%") {
		t.Fatalf("expected '40%%' in FLOOR cell:\n%s", view)
	}
	if !strings.Contains(view, "under floor") {
		t.Fatalf("expected 'under floor' on paused window:\n%s", view)
	}
	if strings.Contains(view, "f set worker floor") {
		t.Fatalf("did not expect 'f set worker floor' in title when a floor is set:\n%s", view)
	}

	// Press 'f', set floor to 0 (off)
	m = keys(m, "f", "backspace", "backspace", "0", "enter")
	if m.snap.WorkerFloors.Floor("anthropic") != 0 {
		t.Fatalf("expected floor 0, got %d", m.snap.WorkerFloors.Floor("anthropic"))
	}
	view = ansi.Strip(m.testView())
	if !strings.Contains(view, "f set worker floor") {
		t.Fatalf("expected 'f set worker floor' hint again once floor is off:\n%s", view)
	}
	if strings.Contains(view, "FLOOR") {
		t.Fatalf("did not expect FLOOR column when floor is off:\n%s", view)
	}
	if strings.Contains(view, "under floor") {
		t.Fatalf("should not be paused when floor is off:\n%s", view)
	}
}
