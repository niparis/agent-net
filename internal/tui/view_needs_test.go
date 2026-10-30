package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// waiting asks the fake session a stays waiting since the given time.
func waiting(a *protocol.AgentView, since time.Duration, kind, text string) {
	a.Waiting = &protocol.Waiting{Kind: kind, Text: text, Since: t0.Add(since).Unix()}
}

// needsModel is the fake screen with two sessions that wait for the owner.
func needsModel(w, h int) Model {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		switch snap.Agents[i].SessionID {
		case "s_0000000000000002":
			waiting(&snap.Agents[i], -12*time.Minute, protocol.WaitingQuestion, "Merge tenebrae-nexus/truenas-stacks #817?")
		case "s_0000000000000004":
			waiting(&snap.Agents[i], -3*time.Minute, protocol.WaitingPermission, "Bash(git push origin main)")
		}
	}
	src := &fakeSource{snap: snap, events: fakeEvents()}
	src.usage, src.counts, src.tops = fakeUsage(), fakeCounts(), fakeTops()
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

func TestNeedsSectionIsAboveTheMachinesAndOldestFirst(t *testing.T) {
	m := needsModel(100, 30)
	text := m.View()
	ls := lines(text)
	head, machine := -1, -1
	for i, l := range ls {
		switch {
		case strings.Contains(l, "Needs you (2)"):
			head = i
		case strings.Contains(l, "Mac (here)"):
			machine = i
		}
	}
	if head < 0 || machine < 0 {
		t.Fatalf("section or machine header missing:\n%s", text)
	}
	first, second := head+1, head+2
	if !(head < first && first < second && second < machine) {
		t.Fatalf("rows must sit above the machines, oldest first: head=%d machine=%d first=%d second=%d\n%s",
			head, machine, first, second, text)
	}
	if !strings.Contains(ls[first], `"Merge tenebrae-nexus/truenas-stacks #817?"`) || !strings.Contains(ls[first], "12m") {
		t.Errorf("question row: %q", ls[first])
	}
	if !strings.Contains(ls[second], "permission: Bash(git push origin main)") || !strings.Contains(ls[second], "3m") {
		t.Errorf("permission row: %q", ls[second])
	}
	// Both sessions also say "needs you" in the main table.
	if got := strings.Count(text, "needs you"); got < 2 {
		t.Fatalf("STATUS does not say needs you: %d times\n%s", got, text)
	}
}

func TestNeedsSectionIsHiddenWhenNobodyWaits(t *testing.T) {
	if got := newModel(80, 24).View(); strings.Contains(got, "Needs you") {
		t.Fatalf("empty section shown:\n%s", got)
	}
	if n := newModel(80, 24).needsHeight(); n != 0 {
		t.Fatalf("empty section takes %d rows", n)
	}
}

func TestNeedsRowsFollowTheFilter(t *testing.T) {
	m := keys(needsModel(100, 30), "/", "c", "i", "-", "r", "u", "n", "n", "e", "r", "enter")
	if got := len(m.needsRows()); got != 1 {
		t.Fatalf("filter left %d needs rows, want 1:\n%s", got, m.View())
	}
	if !strings.Contains(m.View(), "Needs you (1)") {
		t.Fatalf("the heading did not follow the filter:\n%s", m.View())
	}
}

// The keys act on the selected row of the section, like on a table row.
func TestNeedsKeysWorkLikeATableRow(t *testing.T) {
	m := keys(needsModel(100, 30), "g")
	if m.selected != "s_0000000000000002" {
		t.Fatalf("home selected %q, want the oldest wait", m.selected)
	}
	m = keys(m, "m")
	if m.mode != modeCompose || m.composeTo != "s_0000000000000002" {
		t.Fatalf("m did not compose to the waiting session: mode=%v to=%q", m.mode, m.composeTo)
	}
}

func TestNeedsStatusCell(t *testing.T) {
	a := agent("s_1", "n_mac", "Mac", "claude", "alice", func(a *protocol.AgentView) {
		waiting(a, -time.Minute, protocol.WaitingPermission, "Bash(ls)")
	})
	m := newModel(100, 30)
	if got := m.statusCell(a); got != "needs you" {
		t.Errorf("status = %q", got)
	}
	a.Waiting = nil
	a.Online = false
	if got := m.statusCell(a); got != "off" {
		t.Errorf("offline status = %q", got)
	}
}

func TestViewGoldenNeeds(t *testing.T) {
	golden(t, "view_needs_100x30", needsModel(100, 30).View())
}

func TestNeedsViewFitsEverySize(t *testing.T) {
	m := needsModel(80, 24)
	for w := 30; w <= 140; w += 11 {
		for h := 8; h <= 40; h += 4 {
			v := send(m, tea.WindowSizeMsg{Width: w, Height: h})
			out := v.View()
			if ls := lines(out); len(ls) > max(h, 10) {
				t.Fatalf("%dx%d: %d lines", w, h, len(ls))
			}
			for i, l := range lines(out) {
				if ansi.StringWidth(l) > max(w, 40) {
					t.Fatalf("%dx%d line %d is %d columns wide: %q", w, h, i, ansi.StringWidth(l), l)
				}
			}
		}
	}
}

func TestNeedsStatusDoesNotPushTablePastWidth(t *testing.T) {
	for _, line := range lines(needsModel(100, 30).View()) {
		if strings.Contains(line, "NAME") && strings.Contains(line, "STATUS") && strings.HasSuffix(strings.TrimSpace(line), "…") {
			t.Fatalf("table heading is clipped: %q", line)
		}
	}
}
