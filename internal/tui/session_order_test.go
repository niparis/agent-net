package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestActivityBand(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		age            time.Duration
		previous       int
		online, urgent bool
		want           int
	}{
		{"busy", time.Hour, 3, true, true, 0},
		{"recent", time.Minute, 3, true, false, 1},
		{"first boundary margin", 11 * time.Minute, 1, true, false, 1},
		{"first boundary passed", 13 * time.Minute, 1, true, false, 2},
		{"second boundary margin", 65 * time.Minute, 2, true, false, 2},
		{"second boundary passed", 71 * time.Minute, 2, true, false, 3},
		{"old", 25 * time.Hour, -1, true, false, 4},
		{"offline", time.Minute, 1, false, false, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := activityBand(now, now.Add(-tc.age), tc.previous, tc.online, tc.urgent); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestOrderFreezeAndSelection(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = nil
	for i, name := range []string{"first", "second"} {
		snap.Agents = append(snap.Agents, agent(name, "n_mac", "Mac", "claude", name, func(a *protocol.AgentView) {
			a.FirstSeen = t0.Add(time.Duration(i) * time.Second).Unix()
			a.LastSeen = t0.Add(-time.Hour).Unix()
		}))
	}
	now := t0
	m := treeModel(80, 24, snap)
	m.now = func() time.Time { return now }
	m.selected = "first"
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	selected := m.selected
	snap.Agents[1].Status = "busy"
	m = refresh(m, &fakeSource{snap: snap})
	if got := strings.Join(names(m), ","); got != "first,second" {
		t.Fatalf("order during freeze: %s", got)
	}
	now = now.Add(3 * time.Second)
	m = refresh(m, &fakeSource{snap: snap})
	if got := strings.Join(names(m), ","); got != "second,first" {
		t.Fatalf("order after freeze: %s", got)
	}
	if m.selected != selected {
		t.Fatalf("selection changed from %s to %s", selected, m.selected)
	}
}

func TestMessageMovesSession(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = nil
	for i, name := range []string{"first", "second"} {
		snap.Agents = append(snap.Agents, agent(name, "n_mac", "Mac", "claude", name, func(a *protocol.AgentView) {
			a.FirstSeen = t0.Add(time.Duration(i) * time.Second).Unix()
			a.LastSeen = t0.Add(-2 * time.Hour).Unix()
		}))
	}
	m := treeModel(80, 24, snap)
	events := []protocol.FeedEvent{{Event: protocol.Event{ID: 13, Kind: "msg.stored", Time: t0}, From: "second"}}
	m = refresh(m, &fakeSource{snap: snap, events: events})
	if got := strings.Join(names(m), ","); got != "second,first" {
		t.Fatalf("message order: %s", got)
	}
}

func TestNewSessionEntersTopOfBand(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{agent("first", "n_mac", "Mac", "claude", "first", nil)}
	m := treeModel(80, 24, snap)
	snap.Agents = append(snap.Agents, agent("second", "n_mac", "Mac", "claude", "second", nil))
	m = refresh(m, &fakeSource{snap: snap})
	if got := strings.Join(names(m), ","); got != "second,first" {
		t.Fatalf("new session order: %s", got)
	}
}

func TestNewBusySessionDoesNotMoveRowsDuringFreeze(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{agent("first", "n_mac", "Mac", "claude", "first", nil)}
	m := treeModel(80, 24, snap)
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	snap.Agents = append(snap.Agents, agent("second", "n_mac", "Mac", "claude", "second", func(a *protocol.AgentView) { a.Status = "busy" }))
	m = refresh(m, &fakeSource{snap: snap})
	if got := strings.Join(names(m), ","); got != "first,second" {
		t.Fatalf("frozen order: %s", got)
	}
}
