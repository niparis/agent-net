package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A live session that lost its agent-net tools shows a small marker in its
// row, and the detail panel says how to reconnect. No other row is marked.
func TestSessionToolsDisconnectedScene(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := quietFixtures(newModel(size.w, size.h))
		m.activeOnly = false
		a := &m.snap.Agents[0]
		a.Status = "idle"
		a.Profile.Task = "ingest the forge batch"
		a.ToolsVersion = protocol.ToolsDisconnected
		m.selected = a.SessionID
		view := m.testView()
		if !strings.Contains(view, "no tools") {
			t.Fatalf("%dx%d view lacks the marker:\n%s", size.w, size.h, view)
		}
		if strings.Count(view, "no tools") != 1 {
			t.Fatalf("%dx%d: marker on more than one row:\n%s", size.w, size.h, view)
		}
		golden(t, "sessions_tools_disconnected_"+itoa(size.w)+"x"+itoa(size.h), view)
	}
}

func TestSessionToolsConnectedHasNoMarker(t *testing.T) {
	m := quietFixtures(newModel(120, 34))
	m.activeOnly = false
	m.snap.Agents[0].ToolsVersion = "v0.5.0"
	if view := m.testView(); strings.Contains(view, "no tools") {
		t.Fatalf("marker on a connected session:\n%s", view)
	}
}

func TestDetailPaneSaysHowToReconnectDisconnectedTools(t *testing.T) {
	m, src := workerModel(140, 40)
	const target = "s_0000000000000001"
	set := func(v string) Model {
		for i := range src.snap.Agents {
			if src.snap.Agents[i].SessionID == target {
				src.snap.Agents[i].ToolsVersion = v
			}
		}
		return selectID(refresh(m, src), target)
	}
	flat := func(m Model) string { return strings.Join(strings.Fields(detailText(m)), " ") }
	if s := flat(set("v0.5.0")); strings.Contains(s, "tools disconnected") {
		t.Errorf("connected tools show the warning:\n%s", s)
	}
	// The pane is two columns, so the line wraps between words and the column
	// border (a "│" in the flattened text) can fall inside it.
	s := strings.ReplaceAll(flat(set(protocol.ToolsDisconnected)), " │ ", " ")
	for _, want := range []string{"agent-net tools disconnected; the CTO can restart it (session_change restart), or type /mcp to reconnect"} {
		if !strings.Contains(s, want) {
			t.Errorf("detail lacks %q:\n%s", want, s)
		}
	}
}
