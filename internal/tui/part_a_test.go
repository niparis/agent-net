package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/niparis/agent-net/internal/protocol"
)

// Part A: the main table. Its columns, its stable order and its filters.

// ---- columns ----

func TestTaskCellUsesAnnouncementThenTitleThenNothing(t *testing.T) {
	plain := agent("s_0000000000000010", "n_mac", "Mac", "claude", "web", nil)
	plain.Title = "Fix the checkout"
	if got := taskText(plain); got != "Fix the checkout" {
		t.Errorf("title fallback = %q", got)
	}
	plain.Profile.Task = "add the coupon field"
	if got := taskText(plain); got != "add the coupon field" {
		t.Errorf("announced task = %q", got)
	}
	plain.Profile.Task = ""
	plain.Title = "web"
	if got := taskText(plain); got != "" {
		t.Errorf("a title equal to the name must show nothing, got %q", got)
	}
}

func TestWorkerRowShowsItsTaskOrAFallback(t *testing.T) {
	w := agent("s_0000000000000011", "n_mac", "Mac", "opencode", "tenebrae", func(a *protocol.AgentView) {
		a.Worker = &protocol.WorkerInfo{ID: "w_c641d30bf14ec9d6", Owner: "s_0000000000000001", Task: "deploy the routes"}
	})
	if got := rowName(w); got != "deploy the routes" {
		t.Errorf("worker row name = %q", got)
	}
	if got := taskText(w); got != "" {
		t.Errorf("a worker's TASK repeats its name: %q", got)
	}
	// The name a plugin reports does not replace the task.
	w.Profile.Name = "opencode worker tenebrae"
	if got := rowName(w); got != "deploy the routes" {
		t.Errorf("the plugin name won over the task: %q", got)
	}
	// Without a task the row says which worker it is, by its worker ID.
	w.Worker.Task = ""
	if got := rowName(w); got != "worker opencode w_c641d30b" {
		t.Errorf("worker fallback = %q", got)
	}
	w.Title = "tenebrae deploy"
	if got := taskText(w); got != "tenebrae deploy" {
		t.Errorf("a worker without a task should fall back to its title, got %q", got)
	}
}

func TestWorkerDetailShowsItsWorkerID(t *testing.T) {
	m := newModel(120, 40)
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == "s_0000000000000002" {
			m.snap.Agents[i].Worker = &protocol.WorkerInfo{ID: "w_c641d30bf14ec9d6", Owner: "s_0000000000000001", Project: "strategies"}
		}
	}
	m.selected = "s_0000000000000002"
	v := detailText(m)
	if !strings.Contains(v, "w_c641d30bf14ec9d6") {
		t.Fatalf("the detail pane lacks the worker ID:\n%s", v)
	}
}

// A worker's detail pane says how full its context window is and how many
// tokens it has used. The numbers are not there for a session that is not a
// worker, or before the harness has reported them.
func TestWorkerDetailShowsContextAndTokens(t *testing.T) {
	m := newModel(120, 40)
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == "s_0000000000000002" {
			m.snap.Agents[i].Worker = &protocol.WorkerInfo{ID: "w_c641d30bf14ec9d6", Owner: "s_0000000000000001",
				Project: "strategies", ContextTokens: 84000, ContextLimit: 1000000, Tokens: 345000}
		}
	}
	m.selected = "s_0000000000000002"
	if v := detailText(m); !strings.Contains(v, "context  84k / 1M (8%)  tokens 345k") {
		t.Fatalf("the detail pane lacks the context line:\n%s", v)
	}
	// Without a context window only the token total is there.
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == "s_0000000000000002" {
			m.snap.Agents[i].Worker.ContextTokens, m.snap.Agents[i].Worker.ContextLimit = 0, 0
		}
	}
	if v := detailText(m); !strings.Contains(v, "tokens   345k") || strings.Contains(v, "context  ") {
		t.Fatalf("the detail pane without a context window:\n%s", v)
	}
}

func TestTrustIsAlwaysAColumn(t *testing.T) {
	for _, avail := range []int{46, 60, 80, 120} {
		if !hasColumn(columns(avail, 12), "TRUST") {
			t.Errorf("%d columns: TRUST is gone", avail)
		}
	}
}

func TestTableShowsTheNewColumnsAndNotTheMovedOnes(t *testing.T) {
	m := newModel(120, 34)
	out := m.View()
	for _, want := range []string{"NAME", "AGENT", "PROJECT", "MODEL", "STATUS", "TRUST", "CPU", "RAM", "TASK"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing column %q", want)
		}
	}
	for _, gone := range []string{" BRANCH ", " MODE ", " PROC ", " FUNCTION"} {
		if strings.Contains(out, gone) {
			t.Errorf("the table still has %q:\n%s", gone, out)
		}
	}
	// They are in the detail pane instead.
	m.selected = "s_0000000000000002"
	v := detailText(m)
	for _, want := range []string{"branch   feature/risk-model", "model    claude-sonnet-5-5, background"} {
		if !strings.Contains(v, want) {
			t.Errorf("the detail pane lacks %q:\n%s", want, v)
		}
	}
}

// ---- order ----

func TestSessionsAreOrderedByFirstSeenAndStay(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = nil
	at := t0.Add(-time.Hour)
	add := func(id, name string, seen time.Duration) {
		snap.Agents = append(snap.Agents, agent(id, "n_mac", "Mac", "claude", name, func(a *protocol.AgentView) {
			a.FirstSeen, a.LastSeen = at.Add(seen).Unix(), t0.Unix()
		}))
	}
	// Listed out of order on purpose.
	add("s_00000000000000a3", "third", 30*time.Minute)
	add("s_00000000000000a1", "first", 10*time.Minute)
	add("s_00000000000000a2", "second", 20*time.Minute)
	m := treeModel(120, 34, snap)
	if got := strings.Join(names(m), ","); got != "first,second,third" {
		t.Fatalf("order %s", got)
	}
	// Activity does not re-order: only the first seen counts.
	for i := range snap.Agents {
		if snap.Agents[i].Profile.Name == "third" {
			snap.Agents[i].LastSeen = t0.Add(-time.Minute).Unix()
			snap.Agents[i].Status = "busy"
		}
	}
	m = refresh(m, &fakeSource{snap: snap, events: fakeEvents()})
	if got := strings.Join(names(m), ","); got != "first,second,third" {
		t.Fatalf("order after activity %s", got)
	}
}

func TestMachinesAreThisOneFirstThenByName(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes[0].Hostname = "zeta" // this machine, last by name
	for i := range snap.Nodes {
		if snap.Nodes[i].NodeID == "n_vm" {
			snap.Nodes[i].Hostname = "alpha"
		}
	}
	m := treeModel(120, 34, snap)
	gs := m.groups()
	if len(gs) != 2 || gs[0].nodeID != "n_mac" || gs[1].nodeID != "n_vm" {
		t.Fatalf("machine order %v, %v; want this machine, then alpha", gs[0].nodeID, gs[1].nodeID)
	}
}

func TestNewSessionAppearsAtTheBottomOfItsMachine(t *testing.T) {
	m := newModel(120, 34)
	before := names(m)
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	src.snap.Agents = append(src.snap.Agents, agent("s_00000000000000f1", "n_mac", "Mac", "pi", "just-started", func(a *protocol.AgentView) {
		a.FirstSeen, a.LastSeen = t0.Unix(), t0.Unix()
	}))
	m = refresh(m, src)
	// It is in the Mac group, after web, before the other machine.
	var mac []string
	for _, a := range m.groups()[0].agents {
		mac = append(mac, a.Profile.Name)
	}
	if strings.Join(mac, ",") != strings.Join(before[:3], ",")+",just-started" {
		t.Fatalf("Mac group %v, want %v plus the new one", mac, before[:3])
	}
}

// ---- filters ----

func TestActiveOnlyHidesQuietIdleSessions(t *testing.T) {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		if snap.Agents[i].Profile.Name == "web" {
			snap.Agents[i].Status = "idle"
			snap.Agents[i].LastSeen = t0.Add(-2 * time.Hour).Unix()
		}
	}
	src := &fakeSource{snap: snap, events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	if !m.idleLong(snap.Agents[2]) {
		t.Fatal("web is not quiet by the rule")
	}
	if !strings.Contains(strings.Join(names(m), ","), "web") {
		t.Fatal("the active filter is on by default")
	}
	m = keys(m, "a")
	if strings.Contains(strings.Join(names(m), ","), "web") {
		t.Fatalf("web was not hidden: %v", names(m))
	}
	v := m.View()
	if !strings.Contains(v, "filter: active") {
		t.Fatalf("the header does not show the filter:\n%s", v)
	}
	if !strings.Contains(v, "2 sessions · 1 hidden") {
		t.Fatalf("the machine heading does not say what is hidden:\n%s", v)
	}
	// It does not hide busy sessions, however old their last report.
	for i := range snap.Agents {
		if snap.Agents[i].Profile.Name == "tenebrae-53" {
			snap.Agents[i].LastSeen = t0.Add(-3 * time.Hour).Unix()
		}
	}
	m = refresh(m, src)
	if !strings.Contains(strings.Join(names(m), ","), "tenebrae-53") {
		t.Fatal("a busy session was hidden as quiet")
	}
}

func TestActiveFilterCombinesWithTheTextFilterInTheHeader(t *testing.T) {
	m := keys(newModel(120, 34), "a", "/", "t", "e", "enter")
	v := m.View()
	if !strings.Contains(v, "filter: active, /te") {
		t.Fatalf("filters not listed together:\n%s", v)
	}
}

func TestIdleSessionsAreDim(t *testing.T) {
	snap := fakeSnapshot()
	for i := range snap.Agents {
		if snap.Agents[i].Profile.Name == "web" {
			snap.Agents[i].LastSeen = t0.Add(-2 * time.Hour).Unix()
		}
	}
	src := &fakeSource{snap: snap, events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	m = settle(refresh(m, src))
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	for _, l := range lines(m.View()) {
		if strings.Contains(l, "web") {
			if !strings.Contains(l, "\x1b[2m") {
				t.Fatalf("the quiet session is not dim: %q", l)
			}
			return
		}
	}
	t.Fatal("no web row")
}

func TestProjectSourceReadsAsWords(t *testing.T) {
	m := newModel(120, 40)
	m.selected = "s_0000000000000001" // tenebrae: a project both registered and seen
	v := detailText(m)
	if !strings.Contains(v, "project source: seen in a session and registered") {
		t.Fatalf("the project source does not read as words:\n%s", v)
	}
}

// ---- layout ----

func TestListUsesItsHeightWithoutAGapBeforeTheDetail(t *testing.T) {
	m := newModel(120, 50)
	l := m.layout()
	if l.sessions < len(m.tableRows()) {
		t.Fatalf("table has only %d rows", l.sessions)
	}
}
