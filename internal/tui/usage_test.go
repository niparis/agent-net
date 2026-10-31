package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

const gib = uint64(1 << 30)

// ---- fake CPU and memory data ----

func tree(pid int, harness string, cpu, cpuAvg float64, rss uint64, procs int, sessions ...string) protocol.TreeUsage {
	return protocol.TreeUsage{PID: pid, Harness: harness, Sessions: sessions, CPU: cpu, CPUAvg: cpuAvg, RSS: rss, RSSAvg: rss, Procs: procs, Samples: 60}
}

// fakeUsage is what the daemon of the Mac says: its own report live, and the
// build VM's through the gateway. old-box sent none.
func fakeUsage() *Usage {
	return &Usage{
		NodeID: "n_mac", Hostname: "Mac",
		Local: &protocol.UsageReport{
			Machine: protocol.MachineUsage{Cores: 10, CPU: 12.4, CPUAvg: 8.2, MemUsed: gib * 91 / 10, MemUsedAvg: gib * 9, MemTotal: 16 * gib, DiskFree: 180 * gib,
				Load: [3]float64{1.24, 1.1, 0.9}, Procs: 520, Samples: 60},
			Trees: []protocol.TreeUsage{
				tree(4242, "claude", 38, 20, gib*12/10, 14, "s_0000000000000001"),
				tree(4300, "claude", 240, 180, gib*46/10, 34, "s_0000000000000002"),
			},
		},
		Gateway: &protocol.UsageAnswer{Nodes: []protocol.UsageNode{{
			NodeID: "n_vm", Hostname: "build-vm", AgeSeconds: 4,
			Usage: protocol.UsageReport{
				Machine: protocol.MachineUsage{Cores: 4, CPU: 71, CPUAvg: 40, MemUsed: gib * 32 / 10, MemUsedAvg: gib * 3, MemTotal: 8 * gib, DiskFree: 31 * gib,
					Load: [3]float64{0.8, 0.7, 0.6}, Procs: 140, Samples: 60},
				Trees: []protocol.TreeUsage{
					tree(981, "pi", 5, 3, 300<<20, 3, "s_0000000000000004"),
					tree(990, "pi", 0.2, 0.1, 80<<20, 2, "s_0000000000000005"),
				},
			},
		}}},
	}
}

func fakeCounts() *protocol.SessionCounts {
	return &protocol.SessionCounts{Online: 12, Offline24h: 5, OfflineTotal: 134}
}

func proc(pid int, cmd, args string, cpu float64, rss uint64) protocol.ProcUsage {
	return protocol.ProcUsage{PID: pid, Cmd: cmd, Args: args, CPU: cpu, RSS: rss}
}

func fakeTops() map[string]protocol.UsageTop {
	mk := func(id string, t protocol.TreeUsage) protocol.UsageTop {
		return protocol.UsageTop{SessionID: id, Tree: t,
			ByCPU: []protocol.ProcUsage{
				proc(4301, "node", "node /srv/app/dev-server.js --port 3000", 180.5, 900<<20),
				proc(4300, "claude", "claude --resume", 40, 600<<20),
				proc(4402, "python3", "python3 backtest.py --years 10", 18, 1200<<20),
				proc(4410, "cargo", "cargo watch -x test", 2.5, 300<<20),
				proc(4500, "sleep", "sleep 600", 0, 1<<20),
			},
			ByRSS: []protocol.ProcUsage{
				proc(4402, "python3", "python3 backtest.py --years 10", 18, 1200<<20),
				proc(4301, "node", "node /srv/app/dev-server.js --port 3000", 180.5, 900<<20),
				proc(4300, "claude", "claude --resume", 40, 600<<20),
				proc(4410, "cargo", "cargo watch -x test", 2.5, 300<<20),
				proc(4501, "bash", "bash -l", 0, 4<<20),
			},
		}
	}
	return map[string]protocol.UsageTop{
		"s_0000000000000002": mk("s_0000000000000002", tree(4300, "claude", 240, 180, gib*46/10, 34, "s_0000000000000002")),
		"s_0000000000000001": mk("s_0000000000000001", tree(4242, "claude", 38, 20, gib*12/10, 14, "s_0000000000000001")),
	}
}

// openCodeSnapshot adds two OpenCode sessions that share one service process.
func openCodeSnapshot() (Snapshot, *Usage) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents,
		agent("s_0000000000000007", "n_mac", "Mac", "opencode", "docs", func(a *protocol.AgentView) { a.Mode = protocol.ModeUnknown }),
		agent("s_0000000000000008", "n_mac", "Mac", "opencode", "api", func(a *protocol.AgentView) { a.Mode = protocol.ModeUnknown; a.Status = "busy" }))
	u := fakeUsage()
	shared := tree(777, "opencode", 22, 15, gib*21/10, 9, "s_0000000000000003", "s_0000000000000007", "s_0000000000000008")
	shared.Shared = true
	u.Local.Trees = append(u.Local.Trees, shared)
	return snap, u
}

func modelWith(snap Snapshot, u *Usage, w, h int) Model {
	src := &fakeSource{snap: snap, events: fakeEvents(), usage: u, counts: fakeCounts(), tops: fakeTops()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

// ---- golden files ----

func TestGoldenMainWithUsage(t *testing.T) {
	golden(t, "usage_80x24", newModel(80, 24).View())
	golden(t, "usage_120x40", newModel(120, 40).View())
}

func TestGoldenDetailWithTopProcesses(t *testing.T) {
	golden(t, "detail_80x24", keys(newModel(80, 24), "d").View())
	golden(t, "detail_120x40", keys(newModel(120, 40), "d").View())
}

func TestGoldenStaleUsage(t *testing.T) {
	m := newModel(80, 24)
	m.now = func() time.Time { return t0.Add(2 * time.Minute) }
	golden(t, "stale_80x24", m.View())
	m = newModel(120, 40)
	m.now = func() time.Time { return t0.Add(2 * time.Minute) }
	golden(t, "stale_120x40", m.View())
}

func TestGoldenOpenCodeSharedTree(t *testing.T) {
	snap, u := openCodeSnapshot()
	golden(t, "shared_80x24", modelWith(snap, u, 80, 24).View())
	m := modelWith(snap, u, 120, 40)
	m.selected = "s_0000000000000003" // web, on the shared tree
	m.ensureSelection()
	m = settle(m)
	golden(t, "shared_120x40", m.View())
}

func TestHeaderQuotaWarning(t *testing.T) {
	snap, u := openCodeSnapshot()
	snap.WorkerProfiles = append(snap.WorkerProfiles, protocol.WorkerProfile{Harness: "codex"})
	u.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 92}}}}}
	m := modelWith(snap, u, 120, 40)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "codex 92% week · 3") || strings.Contains(v, "quotas:") {
		t.Fatal(v)
	}
	u.Local.Quotas.Providers[0].Windows[0].PercentUsed = 80
	if strings.Contains(ansi.Strip(modelWith(snap, u, 120, 40).View()), "codex 80%") {
		t.Fatal("80% should not warn")
	}
}

func TestGoldenHelp(t *testing.T) {
	v := keys(newModel(100, 40), "?").View()
	for _, want := range []string{"map:", "charters", "u undo"} {
		if !strings.Contains(v, want) {
			t.Errorf("the help lacks %q:\n%s", want, v)
		}
	}
	golden(t, "help_80x24", keys(newModel(80, 24), "?").View())
	golden(t, "help_120x40", keys(newModel(120, 40), "?").View())
}

// ---- model rules ----

func TestHeaderUsesCounts(t *testing.T) {
	m := newModel(100, 30)
	head := lines(m.View())[0]
	if !strings.Contains(head, "12 online · 1 busy") {
		t.Fatalf("header %q", head)
	}
	if strings.Contains(head, "134") || strings.Contains(head, "offline") {
		t.Fatalf("the old offline total is still in the header: %q", head)
	}
	// An older gateway has no counts: the screen counts what it has.
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	old := New(src)
	old.loc, old.now = time.UTC, func() time.Time { return t0 }
	old = send(old, tea.WindowSizeMsg{Width: 100, Height: 30})
	old = refresh(old, src)
	if head := lines(old.View())[0]; !strings.Contains(head, "6 sessions, 5 online · 1 busy") {
		t.Fatalf("header without counts %q", head)
	}
}

func TestOlderDaemonShowsQuestionMarks(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = settle(refresh(m, src))
	v := m.View()
	if !strings.Contains(v, "cpu ?") || !strings.Contains(v, "mem ?") {
		t.Fatalf("no question marks:\n%s", v)
	}
	// It does not ask again every second.
	if m.usageDue() {
		t.Fatal("an older daemon is asked again")
	}
}

func TestUsageIsAskedEveryFiveSeconds(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts()}
	m := New(src)
	now := t0
	m.now = func() time.Time { return now }
	if !m.usageDue() {
		t.Fatal("the first refresh must ask")
	}
	m = refresh(m, src)
	if m.usageDue() {
		t.Fatal("asked again at once")
	}
	now = now.Add(4 * time.Second)
	if m.usageDue() {
		t.Fatal("asked after 4 s")
	}
	now = now.Add(2 * time.Second)
	if !m.usageDue() {
		t.Fatal("not asked after 6 s")
	}
}

func TestStaleUsageReadsAsUnknown(t *testing.T) {
	m := newModel(100, 30)
	if _, st := m.nodeUsage("n_vm"); st != usageOK {
		t.Fatalf("fresh report: %v", st)
	}
	if _, st := m.nodeUsage("n_old"); st != usageNone {
		t.Fatalf("no report: %v", st)
	}
	m.now = func() time.Time { return t0.Add(protocol.UsageStaleSeconds*time.Second + 2*time.Second) }
	if _, st := m.nodeUsage("n_vm"); st != usageStale {
		t.Fatalf("old report: %v", st)
	}
	if _, st := m.nodeUsage("n_mac"); st != usageStale {
		t.Fatalf("this machine's report is old too once the screen stops asking: %v", st)
	}
	v := m.View()
	if !strings.Contains(v, "cpu ?") || !strings.Contains(v, "mem ?") || strings.Contains(v, "12% of 10") {
		t.Fatalf("stale figures are shown:\n%s", v)
	}
}

func TestHeavyTrees(t *testing.T) {
	for _, c := range []struct {
		t    protocol.TreeUsage
		want bool
	}{
		{tree(1, "claude", 10, 5, gib, 20), false},
		{tree(1, "claude", 10, 5, gib, 21), true},
		{tree(1, "claude", 200, 5, gib, 3), true},
		{tree(1, "claude", 10, 5, 4*gib, 3), true},
	} {
		if got := heavy(c.t); got != c.want {
			t.Errorf("heavy(%+v) = %v", c.t, got)
		}
	}
}

func TestSharedTreeIsNotARowAndTopUsesHighestSessionCPU(t *testing.T) {
	snap, u := openCodeSnapshot()
	m := modelWith(snap, u, 120, 40)
	v := m.View()
	if strings.Contains(v, "service, shared by") {
		t.Fatalf("the shared service still has a row of its own:\n%s", v)
	}
	if strings.Contains(v, "opencode service") || strings.Contains(v, "procs") {
		t.Fatalf("the machine row includes shared service process details:\n%s", v)
	}
	if !strings.Contains(v, "top: strategies 240%") {
		t.Fatalf("the machine row does not name its highest CPU session:\n%s", v)
	}
	if n := strings.Count(v, "shared "); n < 3 { // the three sessions on the shared tree
		t.Fatalf("the sessions are not marked shared:\n%s", v)
	}
	for _, row := range m.tableRows() {
		if row.agent.SessionID == "" && !row.header {
			t.Fatal("a table row is not a machine heading or a session")
		}
	}
	// The shared row cannot be selected: every selection is a session.
	m = keys(m, "G")
	if a, ok := m.selectedAgent(); !ok || a.Harness == "" {
		t.Fatal("no session selected")
	}
}

func TestPrivateServiceIsOnTheWorkerRow(t *testing.T) {
	snap := fakeSnapshot()
	const wid = "s_00000000000000f1"
	snap.Agents = append(snap.Agents, agent(wid, "n_mac", "Mac", "opencode", "tenebrae worker", func(a *protocol.AgentView) {
		a.Worker = &protocol.WorkerInfo{ID: "w_1", Owner: "s_0000000000000001", Project: "tenebrae", StartedAt: t0.Add(-2 * time.Minute).Unix(), Task: "deploy the routes"}
		a.Status = "busy"
	}))
	u := fakeUsage()
	private := tree(900, "opencode", 42, 30, gib*12/10, 6, wid)
	private.Shared = true // its own server: shared in name only
	u.Local.Trees = append(u.Local.Trees, private)
	m := modelWith(snap, u, 140, 40)
	var row string
	for _, l := range lines(m.View()) {
		if strings.Contains(l, "deploy the routes") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("the worker row has no task:\n%s", m.View())
	}
	if !strings.Contains(row, "42%") || !strings.Contains(row, "1.2G") || strings.Contains(row, "shared") {
		t.Fatalf("the worker's own server is not on its row: %q", row)
	}
}

func TestMachineHeadingUsesTheNodeNameAndMatchesByNodeID(t *testing.T) {
	snap := fakeSnapshot()
	snap.Nodes[0].Hostname = "NicolassMBP950"
	for i := range snap.Agents {
		if snap.Agents[i].NodeID == "n_mac" {
			snap.Agents[i].Hostname = "Nicolass-MacBook-Pro-950.local"
		}
	}
	m := modelWith(snap, nil, 100, 30)
	v := m.View()
	if !strings.Contains(v, "NicolassMBP950 (here)") {
		t.Fatalf("no heading with the node name:\n%s", v)
	}
	if strings.Contains(v, "Nicolass-MacBook") {
		t.Fatalf("the local hostname leaked into the screen:\n%s", v)
	}
	// "(here)" follows the node ID, not the name.
	m.snap.NodeID = "n_vm"
	v = m.View()
	if strings.Contains(v, "NicolassMBP950 (here)") || !strings.Contains(v, "build-vm (here)") {
		t.Fatalf("(here) did not follow the node ID:\n%s", v)
	}
	// A node that is not in the list falls back to the session's hostname.
	m.snap.Nodes = nil
	if g := m.groups(); len(g) == 0 || !strings.Contains(g[0].host, "build-vm") && !strings.Contains(g[0].host, "Nicolass") {
		t.Fatalf("fallback %+v", g)
	}
}

func TestTopProcessesAreFetchedOnDemandAndCached(t *testing.T) {
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), usage: fakeUsage(), counts: fakeCounts(), tops: fakeTops()}
	m := New(src)
	now := t0
	m.loc, m.now = time.UTC, func() time.Time { return now }
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	m.top = map[string]*topEntry{} // the test drops the commands, so nothing really ran
	if len(src.topReqs) != 0 {
		t.Fatal("top fetched before anyone asked")
	}
	cmd := m.wantTop()
	if cmd == nil {
		t.Fatal("selected session with a tree: no fetch")
	}
	if again := m.wantTop(); again != nil {
		t.Fatal("a second fetch while one runs")
	}
	m = send(m, cmd())
	if len(src.topReqs) != 1 || src.topReqs[0] != m.selected {
		t.Fatalf("requests %v", src.topReqs)
	}
	if !strings.Contains(detailText(m), "node /srv/app") {
		t.Fatalf("top processes not shown:\n%s", m.View())
	}
	// Cached for a few seconds, fetched again after.
	now = now.Add(3 * time.Second)
	if m.wantTop() != nil {
		t.Fatal("fetched again inside the cache time")
	}
	now = now.Add(3 * time.Second)
	if m.wantTop() == nil {
		t.Fatal("not fetched after the cache time")
	}
	// A session with no tree, or an offline one, is not asked about.
	m2 := keys(newModel(100, 30), "down", "down") // web: no tree
	n := len(m2.src.(*fakeSource).topReqs)
	if m2.wantTop() != nil || len(m2.src.(*fakeSource).topReqs) != n {
		t.Fatal("asked for a session with no tree")
	}
}

func TestSelectingASessionStartsItsTopFetch(t *testing.T) {
	m := newModel(100, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("moving the selection did not start the top fetch")
	}
}

// ---- figures ----

func TestUsageFormatting(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{fmtPct(0), "0%"}, {fmtPct(0.4), "<1%"}, {fmtPct(12.4), "12%"}, {fmtPct(240), "240%"},
		{fmtMem(512 << 10), "512K"}, {fmtMem(300 << 20), "300M"}, {fmtMem(gib * 12 / 10), "1.2G"}, {fmtMem(12 * gib), "12G"},
		{fmtMemPair(gib*91/10, 16*gib), "9.1/16G"}, {fmtMemPair(12*gib, 64*gib), "12/64G"},
		{bar(0, 4), "░░░░"}, {bar(0.5, 4), "██░░"}, {bar(1, 4), "████"}, {bar(0.01, 4), "█░░░"}, {bar(2, 4), "████"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestColumnsFitEveryWidth(t *testing.T) {
	names := func(cols []column) string {
		var n []string
		for _, c := range cols {
			n = append(n, c.name)
		}
		return strings.Join(n, ",")
	}
	// Narrow: TRUST and the usage columns stay, TASK has room, the newer
	// columns go first.
	if got := names(columns(78, 12)); !strings.Contains(got, "CPU,RAM") || !strings.Contains(got, "TRUST") ||
		!strings.Contains(got, "TASK") || strings.Contains(got, "BRANCH") || strings.Contains(got, ",MODE,") || strings.Contains(got, "PROC") {
		t.Errorf("78 columns: %s", got)
	}
	if got := names(columns(118, 12)); !strings.Contains(got, "TASK") || strings.Contains(got, "BRANCH") || strings.Contains(got, "FUNCTION") {
		t.Errorf("118 columns: %s", got)
	}
	for avail := 46; avail <= 200; avail += 3 {
		cols := columns(avail, 20)
		sum := len(cols) - 1
		for _, c := range cols {
			sum += c.width
			if c.width < c.min || c.width > c.max {
				t.Errorf("%d: %s is %d wide (%d..%d)", avail, c.name, c.width, c.min, c.max)
			}
		}
		if sum > avail && len(cols) > 5 {
			t.Errorf("%d columns of room, %d used: %s", avail, sum, names(cols))
		}
	}
	// ON is gone, and the old table columns are gone too.
	if got := names(columns(118, 12)); strings.Contains(got, ",ON,") {
		t.Errorf("ON column is back: %s", got)
	}
}

func TestTaskColumnTakesTheRoomAndCutsWithDots(t *testing.T) {
	cols := columns(120, 10)
	var task column
	for _, c := range cols {
		if c.name == "TASK" {
			task = c
		}
	}
	if task.width < 20 {
		t.Fatalf("TASK is only %d wide: %+v", task.width, cols)
	}
	got := cellDots("deploy the new routes", 12)
	if got != "deploy the …" {
		t.Fatalf("cellDots = %q", got)
	}
	if got := cellDots("short", 12); got != "short       " {
		t.Fatalf("cellDots padding = %q", got)
	}
}

func TestEverySizeFitsWithUsage(t *testing.T) {
	snap, u := openCodeSnapshot()
	for w := 40; w <= 200; w += 9 {
		for h := 10; h <= 60; h += 7 {
			for _, ks := range [][]string{nil, {"d"}, {"?"}, {"o", "G"}} {
				m := keys(modelWith(snap, u, w, h), ks...)
				ls := lines(m.View())
				if len(ls) > max(h, 10) {
					t.Fatalf("%dx%d %v: %d lines", w, h, ks, len(ls))
				}
				for i, l := range ls {
					if ansi.StringWidth(l) > max(w, 40) {
						t.Fatalf("%dx%d %v line %d is %d wide: %q", w, h, ks, i, ansi.StringWidth(l), l)
					}
				}
			}
		}
	}
}

func TestKeyLineGivesUpTheLeastUsefulHints(t *testing.T) {
	m := newModel(140, 30)
	if l := m.helpLine(160); !strings.Contains(l, "d detail") || !strings.Contains(l, "? help") || !strings.Contains(l, "enter open session") {
		t.Fatalf("wide key line %q", l)
	}
	l := m.helpLine(60)
	if ansi.StringWidth(l) > 60 || !strings.Contains(l, "q quit") || strings.Contains(l, "d detail") || !strings.Contains(l, "m msg") {
		t.Fatalf("narrow key line %q", l)
	}
}

func TestHelpOverlay(t *testing.T) {
	m := keys(newModel(100, 40), "?")
	v := m.View() + keys(m, "G").View()
	for _, want := range []string{"Keys", "enter the selected session", "q, ctrl+c", "shared"} {
		if !strings.Contains(v, want) {
			t.Errorf("help lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "open the conversation") {
		t.Errorf("help still lists the conversation view:\n%s", v)
	}
	m = keys(m, "x") // other keys do nothing
	if !m.help {
		t.Fatal("a stray key closed the help")
	}
	m = keys(m, "esc")
	if m.help {
		t.Fatal("esc did not close the help")
	}
}

var _ = fmt.Sprint

// FUNCTION is the project description from AGENTS.md (decision 0009). It is in
// the detail pane now. An online session without one says what to add; an
// offline one says nothing.
func TestFunctionIsInTheDetailPane(t *testing.T) {
	const hint = "none: add a `# Project:"
	m := newModel(120, 40)
	m.selected = "s_0000000000000001"
	if v := detailText(m); !strings.Contains(v, "function research") {
		t.Errorf("description missing:\n%s", v)
	}
	for _, id := range []string{"s_0000000000000003", "s_0000000000000005"} {
		m.selected = id
		if v := detailText(m); !strings.Contains(v, hint) {
			t.Errorf("%s: empty description is not explained:\n%s", id, v)
		}
	}
	m.selected = "s_0000000000000001"
	for _, l := range lines(detailText(m)) {
		if strings.Contains(l, "no AGENTS.md") {
			t.Errorf("the table still says no AGENTS.md: %q", l)
		}
	}
	m = keys(m, "o", "G")
	for _, l := range lines(detailText(m)) {
		if strings.Contains(l, "old-session") && strings.Contains(l, "none: add a") {
			t.Errorf("an offline session must not say its description is missing: %q", l)
		}
	}
	// The announced task is not a fallback for the function.
	a := agent("s_x", "n_mac", "Mac", "claude", "x", func(a *protocol.AgentView) { a.Profile.Task = "doing things" })
	if got := functionText(a); got != "no AGENTS.md" {
		t.Errorf("functionText = %q", got)
	}
}

func TestWorkerCountIsSingularForOne(t *testing.T) {
	if plural(1, "worker") != "1 worker" || plural(2, "worker") != "2 workers" || plural(0, "worker") != "0 workers" {
		t.Fatal("plural")
	}
}

func TestQuotaWarningUsesRelevantProviders(t *testing.T) {
	m := newModel(120, 34)
	m.snap.Agents = nil
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Harness: "codex", Model: "gpt-6"}}
	m.usage.raw.Local.Quotas = &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "copilot", Windows: []protocol.QuotaWindow{{ID: "chat", Exhausted: true}}}, {Provider: "codex", Windows: []protocol.QuotaWindow{{ID: "week", PercentUsed: 92}}}}}
	v := ansi.Strip(m.titleLine(120))
	if !strings.Contains(v, "codex 92% week · 3") || strings.Contains(v, "copilot") {
		t.Fatal(v)
	}
	m.snap.WorkerProfiles = nil
	if v = ansi.Strip(m.titleLine(120)); strings.Contains(v, "codex") || strings.Contains(v, "copilot") {
		t.Fatal(v)
	}
	m.snap.Agents = []protocol.AgentView{{Online: true, Harness: "claude", Model: "claude-model"}}
	m.usage.raw.Local.Quotas.Providers = append(m.usage.raw.Local.Quotas.Providers, protocol.QuotaProvider{Provider: "claude", Windows: []protocol.QuotaWindow{{ID: "week", Exhausted: true}}})
	if v = ansi.Strip(m.titleLine(120)); !strings.Contains(v, "claude exhausted week · 3") {
		t.Fatal(v)
	}
}
