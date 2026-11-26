package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// waiting asks the fake session a stays waiting since the given time.
func waiting(a *protocol.AgentView, since time.Duration, kind, text string) {
	a.Waiting = &protocol.Waiting{Kind: kind, Text: text, Since: t0.Add(since).Unix()}
}

const cogforgeMaintenanceID = "s_00000000000000c1"

func waitingTableModel() (Model, *fakeSource) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent(cogforgeMaintenanceID, "n_vm", "build-vm", "claude", "cogforge-maintenance", func(a *protocol.AgentView) {
		a.Status, a.Model = "busy", "anthropic/claude-sonnet-5"
		a.FirstSeen = t0.Add(-19 * time.Minute).Unix()
		waiting(a, -time.Minute, protocol.WaitingQuestion, "Continue maintenance?")
	}))
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return settle(refresh(m, src)), src
}

func tableOrdinal(m Model, id string) int {
	ordinal := 0
	for _, g := range m.groups() {
		for _, a := range g.agents {
			if a.SessionID == id {
				return ordinal
			}
			ordinal++
		}
	}
	return -1
}

func TestWaitingSessionTableRowCanBeReached(t *testing.T) {
	m, _ := waitingTableModel()
	if ordinal := tableOrdinal(m, cogforgeMaintenanceID); ordinal != 0 {
		t.Fatalf("waiting row is not first: %d", ordinal)
	}
	m.selected = cogforgeMaintenanceID
	m.ensureSelection()
	m.move(1)
	if m.selected == cogforgeMaintenanceID {
		t.Fatal("down did not leave the waiting row")
	}
	m.move(-1)
	if m.selected != cogforgeMaintenanceID || m.selIdx != 0 {
		t.Fatalf("up did not reach waiting row: %s at %d", m.selected, m.selIdx)
	}
}

func TestWaitingSelectionMarkerShowsOnOneRow(t *testing.T) {
	m, _ := waitingTableModel()
	m.selected = cogforgeMaintenanceID
	m.selIdx = tableOrdinal(m, cogforgeMaintenanceID)
	if got := strings.Count(m.testView(), "▸ "); got != 1 {
		t.Fatalf("selection marker appears on %d rows, want one:\n%s", got, m.testView())
	}
}

func TestWaitingStatusCell(t *testing.T) {
	a := agent("s_1", "n_mac", "Mac", "claude", "alice", func(a *protocol.AgentView) {
		waiting(a, -time.Minute, protocol.WaitingPermission, "Bash(ls)")
	})
	m := newModel(100, 30)
	if got := m.statusCell(a); got != "needs you" {
		t.Errorf("status = %q", got)
	}
	a.Waiting = nil
	a.Online = false
	if got := m.statusCell(a); got != "ended just now" {
		t.Errorf("offline status = %q", got)
	}
}

func TestWorkerWaitingDoesNotNeedOwner(t *testing.T) {
	m := newModel(80, 24)
	a := protocol.AgentView{SessionID: "s_worker", Harness: "opencode", Online: true, Status: "busy", Worker: &protocol.WorkerInfo{}, Waiting: &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "Blocked"}}
	m.snap.Agents = []protocol.AgentView{a}
	if got := m.statusCell(a); got == "needs you" {
		t.Fatal("worker status needs you")
	}
}

func TestWorkerWaitingScreen(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		name          string
	}{{80, 24, "worker_waiting_80x24"}, {120, 34, "worker_waiting_120x34"}} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(tc.width, tc.height)
			m.snap.Agents = []protocol.AgentView{agent("s_worker", "n_mac", "test-host", "opencode", "blocked-worker", func(a *protocol.AgentView) {
				a.Status = "busy"
				a.Worker = &protocol.WorkerInfo{ID: "w_test"}
				waiting(a, -time.Minute, protocol.WaitingQuestion, "Which branch?")
			})}
			view := ansi.Strip(m.testView())
			if strings.Contains(view, "Needs you") || !strings.Contains(view, "blocked-w") {
				t.Fatalf("unexpected worker screen:\n%s", view)
			}
			golden(t, tc.name, m.testView())
		})
	}
}
