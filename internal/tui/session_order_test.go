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

func TestRestartSortsByLastActiveNotLastSeen(t *testing.T) {
	// After a daemon restart every session is reported in the same second, but
	// only one of them has done something recently. The old order used the
	// report time, then the oldest start.
	snap := fakeSnapshot()
	snap.Agents = nil
	snap.Agents = append(snap.Agents,
		agent("old-start", "n_mac", "Mac", "claude", "old-start", func(a *protocol.AgentView) {
			a.FirstSeen, a.LastSeen = t0.Add(-60*time.Minute).Unix(), t0.Unix()
			a.LastActive = t0.Add(-9 * time.Minute).Unix()
		}),
		agent("new-start", "n_mac", "Mac", "claude", "new-start", func(a *protocol.AgentView) {
			a.FirstSeen, a.LastSeen = t0.Add(-10*time.Minute).Unix(), t0.Unix()
			a.LastActive = t0.Add(-1 * time.Minute).Unix()
		}),
	)
	m := treeModel(80, 24, snap)
	if got := strings.Join(names(m), ","); got != "new-start,old-start" {
		t.Fatalf("order after restart: %s, want newest activity first", got)
	}

	// A re-report after the restart moves LastSeen for every session again;
	// the real activity did not change, so neither does the order.
	now := t0.Add(30 * time.Second)
	m.now = func() time.Time { return now }
	for i := range snap.Agents {
		snap.Agents[i].LastSeen = now.Unix()
	}
	m = refresh(m, &fakeSource{snap: snap})
	if got := strings.Join(names(m), ","); got != "new-start,old-start" {
		t.Fatalf("order after re-report: %s, want unchanged", got)
	}
}

func orderedTableNames(m Model) []string {
	var out []string
	for _, g := range m.groups() {
		for _, a := range g.agents {
			out = append(out, a.Profile.Name)
		}
	}
	return out
}

func TestSessionSelectionFollowsActivityOrderDuringFreeze(t *testing.T) {
	const waitingID = "waiting-session"
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{
		agent(waitingID, "n_mac", "Mac", "claude", "waiting", func(a *protocol.AgentView) {
			a.Status, a.LastSeen = "busy", t0.Add(-3*time.Hour).Unix()
			a.FirstSeen = t0.Add(-time.Minute).Unix()
			waiting(a, -time.Minute, protocol.WaitingQuestion, "Continue?")
		}),
		agent("recent-session", "n_mac", "Mac", "pi", "recent", func(a *protocol.AgentView) {
			a.LastSeen = t0.Add(-2 * time.Hour).Unix()
			a.FirstSeen = t0.Add(-2 * time.Minute).Unix()
		}),
	}
	src := &fakeSource{snap: snap, events: fakeEvents()}
	now := t0
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return now }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	wantOrder := "waiting,recent"
	if got := strings.Join(orderedTableNames(m), ","); got != wantOrder {
		t.Fatalf("initial activity order: %s, want %s", got, wantOrder)
	}
	if m.selected != waitingID || m.selIdx != 0 {
		t.Fatalf("initial selection is %q at %d, want the first table row", m.selected, m.selIdx)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != "recent-session" || m.selIdx != 1 {
		t.Fatalf("down selected %q at %d, want the second table row", m.selected, m.selIdx)
	}
	if got := strings.Join(orderedTableNames(m), ","); got != wantOrder {
		t.Fatalf("order changed on down: %s, want %s", got, wantOrder)
	}

	src.snap.Agents[1].Status = "busy"
	m = refresh(m, src)
	if got := strings.Join(orderedTableNames(m), ","); got != wantOrder {
		t.Fatalf("activity order jumped during freeze: %s, want %s", got, wantOrder)
	}
	if m.selected != "recent-session" || m.selIdx != 1 {
		t.Fatalf("refresh selected %q at %d, want the second table row", m.selected, m.selIdx)
	}

	now = now.Add(3 * time.Second)
	m = refresh(m, src)
	if got := strings.Join(orderedTableNames(m), ","); got != "recent,waiting" {
		t.Fatalf("order did not update after freeze: %s", got)
	}
	if m.selected != "recent-session" || m.selIdx != 0 {
		t.Fatalf("post-freeze selection is %q at %d, want the moved table row", m.selected, m.selIdx)
	}
}

func TestSessionActivityNeverFallsBackToLastSeen(t *testing.T) {
	if got := sessionActive(protocol.AgentView{LastSeen: t0.Unix()}); !got.IsZero() {
		t.Fatalf("report time became activity: %v", got)
	}
}

func TestMultipleActivityUpdatesKeepNewestFirst(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{
		agent("older", "n_mac", "Mac", "pi", "older", func(a *protocol.AgentView) { a.LastActive = t0.Add(-5 * time.Minute).Unix() }),
		agent("newer", "n_mac", "Mac", "pi", "newer", func(a *protocol.AgentView) { a.LastActive = t0.Add(-4 * time.Minute).Unix() }),
	}
	m := treeModel(80, 24, snap)
	snap.Agents[0].LastActive = t0.Add(-2 * time.Minute).Unix()
	snap.Agents[1].LastActive = t0.Add(-time.Minute).Unix()
	m = refresh(m, &fakeSource{snap: snap})
	if got := strings.Join(names(m), ","); got != "newer,older" {
		t.Fatalf("simultaneous activity order: %s", got)
	}
}
