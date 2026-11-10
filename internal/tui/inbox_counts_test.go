package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Decision 0129: the STATUS cell says how much waits for a session, held at
// the gateway and sent but not yet delivered to the harness.
func TestSessionRowShowsGatewayAndInSessionCounts(t *testing.T) {
	m := newModel(120, 34)
	m.activeOnly = false
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == "s_0000000000000001" {
			m.snap.Agents[i].GatewayHeld = 3
			m.snap.Agents[i].InSession = 1
		}
	}
	table := ansi.Strip(strings.Join(m.sessionsTable(120, 20), "\n"))
	row := sessionRow(t, table, "tenebrae-53")
	if !strings.Contains(row, "gw 3 · in 1") {
		t.Fatalf("the row does not show the waiting counts: %q", row)
	}
	golden(t, "sessions_inbox_counts_120", strings.Join(m.sessionsTable(120, 20), "\n"))
}

// A session with nothing waiting keeps its row unchanged.
func TestSessionRowWithoutWaitingHasNoCounts(t *testing.T) {
	m := newModel(120, 34)
	m.activeOnly = false
	table := ansi.Strip(strings.Join(m.sessionsTable(120, 20), "\n"))
	if row := sessionRow(t, table, "tenebrae-53"); strings.Contains(row, "gw ") {
		t.Fatalf("a row with no waiting shows counts: %q", row)
	}
}
