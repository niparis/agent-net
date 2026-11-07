package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

func ctrlP() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
}

func typeFinder(m Model, text string) Model {
	for _, r := range text {
		m = send(m, tea.KeyPressMsg{Text: string(r), Code: r})
	}
	return m
}

func finderNames(m Model) []string {
	var out []string
	for _, a := range m.rankedSessions() {
		out = append(out, rowName(a))
	}
	return out
}

// Exact and prefix name matches come first, then the rest by activity.
func TestRankSessionsPutsNameMatchesFirst(t *testing.T) {
	agents := []protocol.AgentView{
		agent("s_a", "n_mac", "Mac", "claude", "alpha", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-30 * time.Hour).Unix()
		}),
		agent("s_b", "n_mac", "Mac", "claude", "alphabet", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-20 * time.Hour).Unix()
		}),
		agent("s_c", "n_mac", "Mac", "claude", "delta", func(a *protocol.AgentView) {
			a.Profile.Project = "alpha-team"
			a.LastActive = t0.Add(-time.Minute).Unix()
		}),
		agent("s_d", "n_mac", "Mac", "claude", "x-alpha", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-2 * time.Minute).Unix()
		}),
	}
	got := rankSessions("alpha", agents, t0)
	var names []string
	for _, a := range got {
		names = append(names, rowName(a))
	}
	want := []string{"alpha", "alphabet", "delta", "x-alpha"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("ranked %v, want %v", names, want)
	}
}

// One query matches a name, a folder, a machine and a task, fuzzily.
func TestRankSessionsSearchesEveryFieldFuzzily(t *testing.T) {
	agents := []protocol.AgentView{
		agent("s_folder", "n_mac", "Mac", "claude", "one", func(a *protocol.AgentView) {
			a.Cwd = "/Users/n/code/onlyfolder"
		}),
		agent("s_machine", "n_mac", "build-vm-x", "claude", "two", nil),
		agent("s_task", "n_mac", "Mac", "claude", "three", func(a *protocol.AgentView) {
			a.Profile.Task = "backtest v2"
		}),
	}
	for query, id := range map[string]string{
		"onlyfolder": "s_folder",
		"bldvm":      "s_machine",
		"bst2":       "s_task",
	} {
		got := rankSessions(query, agents, t0)
		if len(got) != 1 || got[0].SessionID != id {
			t.Fatalf("query %q matched %+v, want %s", query, got, id)
		}
	}
}

// A temp-folder session is junk: the finder skips it unless the query names it
// exactly.
func TestRankSessionsHidesJunkUnlessTheQueryIsExact(t *testing.T) {
	junk := agent("s_junk", "n_mac", "Mac", "claude", "junk-session", func(a *protocol.AgentView) {
		a.Cwd = "/tmp/agent-net-test/work"
	})
	agents := []protocol.AgentView{junk}
	if got := rankSessions("junk", agents, t0); len(got) != 0 {
		t.Fatalf("junk matched a loose query: %+v", got)
	}
	if got := rankSessions("junk-session", agents, t0); len(got) != 1 || got[0].SessionID != "s_junk" {
		t.Fatalf("exact name did not find junk: %+v", got)
	}
	if got := rankSessions("/tmp/agent-net-test/work", agents, t0); len(got) != 1 {
		t.Fatalf("exact folder did not find junk: %+v", got)
	}
}

// Ended workers are not sessions to find; running workers are.
func TestRankSessionsSkipsEndedWorkers(t *testing.T) {
	ended := agent("s_ended", "n_mac", "Mac", "opencode", "worker ended", func(a *protocol.AgentView) {
		a.Online = false
		a.Worker = &protocol.WorkerInfo{ID: "w_ended", Owner: "s_a"}
	})
	running := agent("s_running", "n_mac", "Mac", "opencode", "worker running", func(a *protocol.AgentView) {
		a.Worker = &protocol.WorkerInfo{ID: "w_running", Owner: "s_a"}
	})
	got := rankSessions("worker", []protocol.AgentView{ended, running}, t0)
	if len(got) != 1 || got[0].SessionID != "s_running" {
		t.Fatalf("ranked %+v", got)
	}
}

// With no query the most recent activity comes first.
func TestRankSessionsEmptyQuerySortsByActivity(t *testing.T) {
	agents := []protocol.AgentView{
		agent("s_old", "n_mac", "Mac", "claude", "old", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-5 * time.Hour).Unix()
		}),
		agent("s_new", "n_mac", "Mac", "claude", "new", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-time.Minute).Unix()
		}),
		agent("s_mid", "n_mac", "Mac", "claude", "mid", func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-time.Hour).Unix()
		}),
	}
	got := rankSessions("", agents, t0)
	var names []string
	for _, a := range got {
		names = append(names, rowName(a))
	}
	if strings.Join(names, ",") != "new,mid,old" {
		t.Fatalf("ranked %v", names)
	}
}

func TestCtrlPOpensTheFinderForEverySession(t *testing.T) {
	m := newModel(120, 34)
	m = send(m, ctrlP())
	if !m.finder {
		t.Fatal("ctrl+p did not open the finder")
	}
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "Find session") {
		t.Fatalf("the overlay has no title:\n%s", view)
	}
	for _, name := range []string{"tenebrae-53", "web", "old-session"} {
		if !strings.Contains(view, name) {
			t.Errorf("the finder does not list %s:\n%s", name, view)
		}
	}
	m = keys(m, "esc")
	if m.finder {
		t.Fatal("esc did not close the finder")
	}
}

func TestFinderTypesASubsequenceAndOpensTheRow(t *testing.T) {
	m := newModel(120, 34)
	m = send(m, ctrlP())
	m = typeFinder(m, "strat")
	if got := finderNames(m); len(got) == 0 || got[0] != "strategies" {
		t.Fatalf("find %q: %v", "strat", got)
	}
	m = typeFinder(m, "eg")
	if got := finderNames(m); len(got) == 0 || got[0] != "strategies" {
		t.Fatalf("subsequence %q: %v", "strateg", got)
	}
	next, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	next = settle(next)
	if cmd == nil {
		t.Fatal("enter did not ask to enter the session")
	}
	next = exec(next, cmd)
	if len(next.src.(*fakeSource).enters) == 0 {
		t.Fatal("enter did not go through the session enter path")
	}
	if next.src.(*fakeSource).enters[0].SessionID != "s_0000000000000002" {
		t.Fatalf("entered %q", next.src.(*fakeSource).enters[0].SessionID)
	}
	if next.finder {
		t.Fatal("the finder stayed open")
	}
}

func TestFinderHidesJunkUnlessTheQueryIsExact(t *testing.T) {
	snap := fakeSnapshot()
	tmp := "/tmp/agent-net-finder-test/work"
	snap.Agents = append(snap.Agents, agent("s_junk", "n_mac", "Mac", "claude", "junk-session", func(a *protocol.AgentView) {
		a.Cwd = tmp
		a.Repo = protocol.RepoInfo{Project: "junk", Worktree: tmp}
	}))
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	m = send(m, ctrlP())
	m = typeFinder(m, "junk")
	if strings.Contains(ansi.Strip(m.testView()), "junk-session") {
		t.Fatalf("junk showed for a loose query:\n%s", ansi.Strip(m.testView()))
	}
	m = keys(m, "backspace", "backspace", "backspace", "backspace")
	m = typeFinder(m, "junk-session")
	if !strings.Contains(ansi.Strip(m.testView()), "junk-session") {
		t.Fatalf("exact query did not show junk:\n%s", ansi.Strip(m.testView()))
	}
}

// A temp-folder session stays off the normal views; only the complete view,
// all with hidden, shows it.
func TestJunkSessionNeedsAllAndHidden(t *testing.T) {
	snap := fakeSnapshot()
	tmp := "/tmp/agent-net-view-test/work"
	snap.Agents = append(snap.Agents, agent("s_junk", "n_mac", "Mac", "claude", "junk-session", func(a *protocol.AgentView) {
		a.Cwd = tmp
		a.Repo = protocol.RepoInfo{Project: "junk", Worktree: tmp}
	}))
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	if containsSession(m, "s_junk") {
		t.Fatal("junk showed in the default active view")
	}
	m = keys(m, "v")
	if containsSession(m, "s_junk") {
		t.Fatal("junk showed in all")
	}
	m = keys(m, "H")
	if !containsSession(m, "s_junk") {
		t.Fatalf("all with hidden did not show junk: %v", names(m))
	}
}

func TestActiveViewKeepsIdleSessionsFromTheLastDay(t *testing.T) {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		if snap.Agents[i].Profile.Name == "web" {
			snap.Agents[i].Status = "idle"
			snap.Agents[i].LastSeen = t0.Add(-2 * time.Hour).Unix()
			snap.Agents[i].LastActive = t0.Add(-2 * time.Hour).Unix()
		}
	}
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	if !containsSession(m, "s_0000000000000003") {
		t.Fatalf("a session active two hours ago was hidden: %v", names(m))
	}

	snap.Agents[2].LastSeen = t0.Add(-25 * time.Hour).Unix()
	snap.Agents[2].LastActive = t0.Add(-25 * time.Hour).Unix()
	src = &fakeSource{snap: snap, events: fakeEvents()}
	m = New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	if containsSession(m, "s_0000000000000003") {
		t.Fatalf("a session idle for a day stayed in active: %v", names(m))
	}
	m = keys(m, "v")
	if !containsSession(m, "s_0000000000000003") {
		t.Fatalf("all did not show the idle session: %v", names(m))
	}
}

func TestVHasTwoViewsAndHShowsHidden(t *testing.T) {
	m := newModel(120, 34)
	if keyAction(contextSessions, "v") != "cycle view" {
		t.Fatal("v is not the view key")
	}
	if keyAction(contextSessions, "H") != "show hidden" {
		t.Fatal("H is not the hidden toggle")
	}
	if got := m.sessionViewName(); got != "active" || !m.activeOnly || m.showOffline {
		t.Fatalf("default view %q active %v offline %v", got, m.activeOnly, m.showOffline)
	}
	m = keys(m, "v")
	if got := m.sessionViewName(); got != "all" || m.activeOnly || !m.showOffline {
		t.Fatalf("v did not show all: %q active %v offline %v", got, m.activeOnly, m.showOffline)
	}
	m = keys(m, "v")
	if got := m.sessionViewName(); got != "active" || !m.activeOnly || m.showOffline {
		t.Fatalf("v did not return to active: %q", got)
	}
	m = keys(m, "H")
	if !m.showHiddenSessions {
		t.Fatal("H did not show hidden sessions")
	}
}

func TestFinderShowsAtMostTwelveRows(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Agents = nil
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("s_%02d", i)
		name := fmt.Sprintf("session-%02d", i)
		age := time.Duration(i) * time.Minute
		m.snap.Agents = append(m.snap.Agents, agent(id, "n_mac", "Mac", "claude", name, func(a *protocol.AgentView) {
			a.LastActive = t0.Add(-age).Unix()
		}))
	}
	m = send(m, ctrlP())
	if got := m.finderRoom(); got != 12 {
		t.Fatalf("finder rows %d, want 12", got)
	}
	view := ansi.Strip(strings.Join(m.finderView(120), "\n"))
	if !strings.Contains(view, "session-00") || !strings.Contains(view, "session-11") {
		t.Fatalf("the first twelve rows are missing:\n%s", view)
	}
	if strings.Contains(view, "session-12") {
		t.Fatalf("the finder drew more than twelve rows:\n%s", view)
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.finderSel != 2 {
		t.Fatalf("down moved to %d", m.finderSel)
	}
}

func TestFinderHasMatchColumnHeader(t *testing.T) {
	m := newModel(120, 34)
	m.finder = true
	m.finderQuery = "st"
	view := ansi.Strip(strings.Join(m.finderView(120), "\n"))
	if !strings.Contains(view, "AGE MATCH") {
		t.Fatalf("finder match column has no header:\n%s", view)
	}
}

func TestFinderGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 40}} {
		m := newModel(size[0], size[1])
		m = send(m, ctrlP())
		m = typeFinder(m, "st")
		golden(t, fmt.Sprintf("finder_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestSessionsViewGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 40}} {
		m := newModel(size[0], size[1])
		golden(t, fmt.Sprintf("sessions_view_active_%dx%d", size[0], size[1]), m.testView())
		m = keys(m, "v")
		golden(t, fmt.Sprintf("sessions_view_all_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestActiveViewUsesLastActivity(t *testing.T) {
	m := New(nil)
	m.now = func() time.Time { return t0 }
	a := protocol.AgentView{Online: true, Status: "idle", LastSeen: t0.Unix(), LastActive: t0.Add(-25 * time.Hour).Unix()}
	if !m.quietInActive(a) {
		t.Fatal("recent machine report must not keep an inactive session visible")
	}
	a.LastSeen = t0.Add(-26 * time.Hour).Unix()
	a.LastActive = t0.Add(-2 * time.Hour).Unix()
	if m.quietInActive(a) {
		t.Fatal("recent activity must keep session visible")
	}
}

func TestFinderCompactOverlay(t *testing.T) {
	m := newModel(80, 24)
	m = send(m, ctrlP())
	m = typeFinder(m, "st")
	if n := len(m.finderView(80)); n != 9 {
		t.Fatalf("four results need nine bordered lines, got %d", n)
	}
	view := ansi.Strip(m.renderView())
	if !strings.Contains(view, "Machines") {
		t.Fatal("finder replaced background")
	}
	if strings.Contains(view, "type to search") {
		t.Fatal("duplicate finder hints")
	}
	if !strings.Contains(ansi.Strip(newModel(80, 24).renderView()), "active (v)") {
		t.Fatal("missing narrow view label")
	}
}

func TestFinderMatchEmphasisAndEmptyState(t *testing.T) {
	plain := "  strategies   "
	styled := highlightFinderName(plain, "st")
	if styled == plain || ansi.Strip(styled) != plain {
		t.Fatal("name matching must add style without changing text")
	}
	m := newModel(80, 24)
	m.finderQuery = "no-such-session-xyz"
	view := ansi.Strip(strings.Join(m.finderView(72), "\n"))
	if !strings.Contains(view, "no sessions match") {
		t.Fatal(view)
	}
}

func TestNarrowViewLabelPreservesModelWidth(t *testing.T) {
	m := newModel(80, 24)
	view := ansi.Strip(m.renderView())
	if !strings.Contains(view, "claude · opus-5") {
		t.Fatal("view label shortened MODEL")
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "active (v)") && !strings.Contains(line, "─") {
			t.Fatal("label must be on machine separator")
		}
	}
}
