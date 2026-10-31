package tui

import (
	"context"
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
	ordinal := tableOrdinal(m, cogforgeMaintenanceID)
	if ordinal <= 0 {
		t.Fatalf("waiting row has no row above it: %d", ordinal)
	}
	var above protocol.AgentView
	seen := 0
	for _, g := range m.groups() {
		for _, a := range g.agents {
			if seen == ordinal-1 {
				above = a
			}
			seen++
		}
	}
	m.selected = above.SessionID
	m.ensureSelection()
	m.move(1)
	if m.selected != cogforgeMaintenanceID {
		t.Fatalf("down from %q skipped the table row and selected %q", above.Profile.Name, m.selected)
	}
	want := len(m.needsRows()) + ordinal
	if m.selIdx != want {
		t.Fatalf("selected position %d, want table position %d", m.selIdx, want)
	}
}

func TestWaitingSelectionMarkerShowsOnOneRow(t *testing.T) {
	m, _ := waitingTableModel()
	m.selected = cogforgeMaintenanceID
	m.selIdx = len(m.needsRows()) + tableOrdinal(m, cogforgeMaintenanceID)
	m.selNeeds = false
	if got := strings.Count(m.View(), "▸ "); got != 1 {
		t.Fatalf("selection marker appears on %d rows, want one:\n%s", got, m.View())
	}
}

func TestRefreshKeepsWaitingTableRowSelected(t *testing.T) {
	m, src := waitingTableModel()
	m.selected = cogforgeMaintenanceID
	m.selIdx = len(m.needsRows()) + tableOrdinal(m, cogforgeMaintenanceID)
	m.selNeeds = false
	// A new waiting row shifts the table section down without changing the
	// selected session's section.
	newWait := agent("s_00000000000000c2", "n_mac", "Mac", "pi", "other-wait", func(a *protocol.AgentView) {
		a.Status = "busy"
		waiting(a, -2*time.Minute, protocol.WaitingPermission, "Allow command?")
	})
	src.snap.Agents = append(src.snap.Agents, newWait)
	m = refresh(m, src)
	want := len(m.needsRows()) + tableOrdinal(m, cogforgeMaintenanceID)
	if m.selected != cogforgeMaintenanceID || m.selIdx != want {
		t.Fatalf("refresh selected %q at %d, want table row %q at %d", m.selected, m.selIdx, cogforgeMaintenanceID, want)
	}
}

func TestNeedsAndTableRowsAreVisitedOnceInOrder(t *testing.T) {
	m, _ := waitingTableModel()
	vis := m.visible()
	if got := strings.Count(strings.Join(ids(m), ","), cogforgeMaintenanceID); got != 2 {
		t.Fatalf("waiting session appears %d times in visible rows, want needs and table rows", got)
	}
	m.move(-1 << 20)
	for i, want := range vis {
		if m.selIdx != i || m.selected != want.SessionID {
			t.Fatalf("row %d selected position=%d session=%q, want %q", i, m.selIdx, m.selected, want.SessionID)
		}
		m.move(1)
	}
}

func selectWaitingRow(m Model, inNeeds bool) Model {
	index := len(m.needsRows()) + tableOrdinal(m, cogforgeMaintenanceID)
	if inNeeds {
		for i, a := range m.needsRows() {
			if a.SessionID == cogforgeMaintenanceID {
				index = i
				break
			}
		}
	}
	m.selected, m.selIdx, m.selNeeds = cogforgeMaintenanceID, index, inNeeds
	return m
}

func TestWaitingRowsUseTheSameSessionActions(t *testing.T) {
	for _, inNeeds := range []bool{true, false} {
		row := "table"
		if inNeeds {
			row = "Needs you"
		}
		t.Run(row, func(t *testing.T) {
			m, src := waitingTableModel()
			m = selectWaitingRow(m, inNeeds)
			if got := detailText(m); !strings.Contains(got, "cogforge-maintenance") {
				t.Fatalf("details for the %s row show a different session:\n%s", row, got)
			}

			answer, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
			if answer.mode != modeNormal || cmd == nil {
				t.Fatalf("enter on the %s row opened mode %v (command %t), want the enter action", row, answer.mode, cmd != nil)
			}

			m, src = waitingTableModel()
			m = selectWaitingRow(m, inNeeds)
			_, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
			if cmd == nil {
				t.Fatal("e returned no enter command")
			}
			cmd()
			if len(src.enters) != 1 || src.enters[0].SessionID != cogforgeMaintenanceID {
				t.Fatalf("e entered sessions %+v", src.enters)
			}
			m, _ = waitingTableModel()
			m = selectWaitingRow(m, inNeeds)
			composed, _ := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
			if composed.mode != modeCompose || composed.composeTo != cogforgeMaintenanceID {
				t.Fatalf("m composed to %q in mode %v", composed.composeTo, composed.mode)
			}
		})
	}
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
	m := needsModel(100, 30)
	golden(t, "view_needs_100x30", m.View())
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

func TestNeedsRowsShowKindAndRightAlignedAge(t *testing.T) {
	m := needsModel(100, 30)
	rows := m.needsLines(100)
	if !strings.Contains(rows[1], "▍") || !strings.Contains(rows[1], "question:") {
		t.Fatalf("question: %q", rows[1])
	}
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(rows[1])), "12m") || !strings.HasSuffix(strings.TrimSpace(ansi.Strip(rows[2])), "3m") {
		t.Fatalf("ages: %q", rows)
	}
}

func TestNeedsMComposesWithoutEntering(t *testing.T) {
	m := needsModel(100, 30)
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == "s_0000000000000002" {
			m.snap.Agents[i].Waiting.Options = []string{"Yes", "No"}
		}
	}
	m = keys(m, "g", "m")
	if m.mode != modeCompose || m.compose.Value() != "" {
		t.Fatalf("compose mode=%v value=%q", m.mode, m.compose.Value())
	}
}

func TestNotifySettingInHelp(t *testing.T) {
	m := newModel(100, 30)
	m.help = true
	if !strings.Contains(strings.Join(m.helpContent(100), "\n"), "desktop notifications: on") {
		t.Fatal("missing notification setting")
	}
}

type notificationSource struct {
	*fakeSource
	disabled bool
}

func (s *notificationSource) NotificationSettings(_ context.Context, disabled *bool) (bool, error) {
	if disabled != nil {
		s.disabled = *disabled
	}
	return s.disabled, nil
}
func TestNotifyHelpToggle(t *testing.T) {
	src := &notificationSource{fakeSource: &fakeSource{}}
	m := newModel(100, 30)
	m.src = src
	m.help = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("no settings command")
	}
	m = next.(Model)
	m = send(m, cmd())
	if !src.disabled || !m.snap.NotificationsDisabled || !strings.Contains(strings.Join(m.helpContent(100), "\n"), "desktop notifications: off") {
		t.Fatalf("toggle did not persist: %+v", m.snap)
	}
}
