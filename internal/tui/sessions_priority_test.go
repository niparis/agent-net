package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func priorityModel() Model {
	m := treeModel(120, 34, Snapshot{})
	m.snap.Agents = []protocol.AgentView{
		{SessionID: "lead", AgentID: "lead", NodeID: "one", Hostname: "desk", Online: true, Status: "idle", Harness: "claude", RepoLeadExplicit: true, Repo: protocol.RepoInfo{Project: "demo"}, Profile: protocol.Profile{Name: "lead"}},
		{SessionID: "kid", NodeID: "two", Hostname: "build", Online: true, Status: "busy", Harness: "codex", Worker: &protocol.WorkerInfo{Owner: "lead"}, Profile: protocol.Profile{Name: "worker"}},
		{SessionID: "merge", Online: true, Status: "idle", Harness: "claude", Type: "merger", ReportsTo: "lead", Repo: protocol.RepoInfo{Project: "demo"}},
		{SessionID: "other", Online: true, Status: "idle", Harness: "claude"},
		{SessionID: "pin", Online: true, Status: "idle", Harness: "claude", PinnedAt: 1},
	}
	m.activeOnly, m.showOffline = true, false
	return m
}

func TestSessionsPriorityGroups(t *testing.T) {
	m := priorityModel()
	m.groupedSessions = true
	g := m.groups()
	if len(g) != 2 || g[0].host != "Working" || g[1].host != "Idle" {
		t.Fatalf("groups: %+v", g)
	}
	// Each row is in the group of its own status: the busy worker is Working
	// while its idle lead stays Idle.
	if got := strings.Join(ids(m), ","); got != "kid,pin,lead,merge" {
		t.Fatalf("order %s", got)
	}
	m.asks = askItems([]protocol.InboxItem{{ID: "q", SessionID: "kid", Kind: "question", Status: "open"}})
	if g = m.groups(); g[0].host != "Needs you" || len(g[0].agents) != 1 {
		t.Fatalf("question did not move only its row: %+v", g)
	}
	m.asks = nil
	m.snap.Agents[1].Status = "needs you"
	if m.groups()[0].host != "Needs you" {
		t.Fatal("needs you status ignored")
	}
}

func TestOfflineWaitingSessionIsIdle(t *testing.T) {
	m := priorityModel()
	m.groupedSessions = true
	m.activeOnly = false
	m.snap.Agents[0].Online = false
	m.snap.Agents[0].Status = "busy"
	m.snap.Agents[0].Waiting = &protocol.Waiting{Kind: protocol.WaitingQuestion, Text: "waiting for owner"}
	m.snap.Agents[1].Status = "idle"
	m.snap.Agents[1].Worker = &protocol.WorkerInfo{Owner: "lead"}
	m.asks = askItems([]protocol.InboxItem{{ID: "q", SessionID: "lead", Kind: "question", Status: "open"}})

	if m.sessionNeedsOwner(m.snap.Agents[0]) {
		t.Fatal("offline session needs owner")
	}
	if got := groupOf(m, "lead").host; got != "Idle" {
		t.Fatalf("offline session group %q, want Idle", got)
	}
	if got := groupOf(m, "kid").host; got != "Idle" {
		t.Fatalf("offline lead tree group %q, want Idle", got)
	}
	for _, g := range m.groups() {
		if g.host == "Needs you" || g.host == "Working" {
			t.Fatalf("offline session appears in %s: %v", g.host, agentIDs(g.agents))
		}
	}
	idle := groupOf(m, "lead")
	if idle.total < 2 || indexOfID(agentIDs(idle.agents), "kid") < 0 {
		t.Fatalf("offline session tree is not counted in Idle: %+v", idle)
	}
	if m.waitingCount() != 1 {
		t.Fatalf("inbox waiting count %d, want 1", m.waitingCount())
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		view := m
		view.width, view.height = size[0], size[1]
		view.haveData = true
		view.problem = ProblemNone
		view.snap.Connected = true
		golden(t, fmt.Sprintf("sessions_offline_waiting_%dx%d", size[0], size[1]), view.testView())
	}
}

func TestSessionsPriorityCollapse(t *testing.T) {
	m := priorityModel()
	m.selected = "kid"
	m.ensureSelection()
	m = keys(m, "z")
	// The busy row comes first among its siblings; the pinned idle lead is
	// not busy, so the pinned pin row leads.
	if got := strings.Join(ids(m), ","); got != "pin,lead" {
		t.Fatalf("collapsed %s", got)
	}
	if m.selected != "lead" {
		t.Fatalf("selection %s", m.selected)
	}
	if view := ansi.Strip(m.testView()); !strings.Contains(view, "+2 (1 busy)") {
		t.Fatalf("missing summary:\n%s", view)
	}
	m = keys(m, "z")
	if len(ids(m)) != 4 {
		t.Fatal("z did not expand")
	}
	m = keys(m, "Z")
	if len(ids(m)) != 2 {
		t.Fatal("Z did not collapse")
	}
	m = keys(m, "Z")
	if len(ids(m)) != 4 {
		t.Fatal("Z did not expand")
	}
}

func TestSessionsPriorityMergerFallback(t *testing.T) {
	m := priorityModel()
	m.snap.Agents[0].RepoLeadExplicit = false
	// The tree still names the lead, so the merger stays under it after the
	// explicit repo-lead mark is gone.
	m.activeOnly = false
	found := false
	for _, group := range m.groups() {
		if mk, ok := group.marks["merge"]; ok {
			found = mk.root == "lead" && mk.prefix == "└─ "
		}
	}
	if !found {
		t.Fatalf("merger not under its tree lead: %v", ids(m))
	}
	m.snap.Agents[2].ReportsTo = ""
	for _, g := range m.groups() {
		if g.marks["merge"].prefix != "" {
			t.Fatal("merger without a tree parent nested")
		}
	}
}

func TestSessionsPriorityIdleAndInbox(t *testing.T) {
	m := priorityModel()
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "1 more idle · v to show") {
		t.Fatalf("missing fold:\n%s", view)
	}
	m.asks = askItems([]protocol.InboxItem{{ID: "q", SessionID: "kid", Kind: "question", Status: "open"}})
	view = ansi.Strip(m.testView())
	if !strings.Contains(view, fmt.Sprintf("Desk: %d to decide", m.waitingCount())) || strings.Contains(view, "waiting in Inbox") {
		t.Fatal(view)
	}
	m = keys(m, "v")
	if indexOfID(ids(m), "other") < 0 {
		t.Fatal("v hides idle")
	}
	m = keys(m, "v")
	if indexOfID(ids(m), "other") >= 0 {
		t.Fatal("v does not fold")
	}
}

// groupOf returns the band that holds a session, or the zero group when the
// session is folded away.
func groupOf(m Model, id string) group {
	for _, g := range m.groups() {
		if indexOfID(agentIDs(g.agents), id) >= 0 {
			return g
		}
	}
	return group{}
}

// An idle merger with no explicit repo lead hangs under the online agent of its
// project that called its open change, not under the session that started it.
func TestIdleMergerFollowsProjectChangeCaller(t *testing.T) {
	m := priorityModel()
	m.snap.Agents[0].RepoLeadExplicit = false
	m.delivery.changes = []deliveryChange{{ID: "ch_one", Repo: "demo", State: protocol.ChangeOpen, Caller: "lead", Updated: 100}}
	g := groupOf(m, "merge")
	if indexOfID(agentIDs(g.agents), "merge") < 0 {
		t.Fatalf("merger is folded: %v", ids(m))
	}
	if mk := g.marks["merge"]; mk.root != "lead" {
		t.Fatalf("merger root %q, want lead: %v", mk.root, ids(m))
	}
	if indexOfID(agentIDs(g.agents), "merge") <= indexOfID(agentIDs(g.agents), "lead") {
		t.Fatalf("merger is not below the caller: %v", ids(m))
	}
}

// A subagent is never folded into the idle count: its whole tree is drawn, and
// the lead stays as context even when it is idle itself.
func TestIdleSubagentStaysInTree(t *testing.T) {
	m := priorityModel()
	m.groupedSessions = true
	m.snap.Agents[0].Status = "busy"
	m.snap.Agents[1].Status = "idle"
	if indexOfID(ids(m), "kid") < 0 {
		t.Fatalf("idle worker under a busy lead folded: %v", ids(m))
	}
	m.snap.Agents[0].Status = "idle"
	got := ids(m)
	if indexOfID(got, "lead") < 0 || indexOfID(got, "kid") < 0 {
		t.Fatalf("idle tree with a subagent folded: %v", got)
	}
	if g := groupOf(m, "kid"); g.host != "Idle" {
		t.Fatalf("worker band %q", g.host)
	}
}

// A merger holding a ready or running step counts as working, even when its
// harness reports idle or shell.
func TestMergerReadyStepMovesItToWorking(t *testing.T) {
	m := priorityModel()
	m.groupedSessions = true
	m.snap.Agents[0].Status = "idle"
	m.snap.Agents[1].Status = "idle"
	m.delivery.changes = []deliveryChange{{ID: "ch_merge", Repo: "demo", State: protocol.ChangeOpen, Step: "merge", StepOwner: "merger", OwnerKind: protocol.OwnerMerger, StepState: protocol.StepStateReady}}
	g := groupOf(m, "merge")
	if g.host != "Working" {
		t.Fatalf("merger band %q, want Working: %+v", g.host, g)
	}
	// Its lead is Idle, so the merger is a top-level row of its own group.
	if mk := g.marks["merge"]; mk.prefix != "" {
		t.Fatalf("merger nested across groups: %+v", mk)
	}
	m.delivery.changes[0].StepState = protocol.StepStateRunning
	m.delivery.changes[0].ClaimedBy = "merge"
	if g := groupOf(m, "merge"); g.host != "Working" {
		t.Fatalf("running merger band %q", g.host)
	}
}

// Top-level idle agents that are not subagents still fold.
func TestTopLevelIdleStillFolds(t *testing.T) {
	m := priorityModel()
	m.activeOnly, m.showOffline = true, false
	if indexOfID(ids(m), "other") >= 0 {
		t.Fatalf("top-level idle agent shown: %v", ids(m))
	}
	if view := ansi.Strip(m.testView()); !strings.Contains(view, "1 more idle · v to show") {
		t.Fatalf("missing idle fold:\n%s", view)
	}
}

func agentIDs(agents []protocol.AgentView) []string {
	var out []string
	for _, a := range agents {
		out = append(out, a.SessionID)
	}
	return out
}

func TestSessionsPriorityGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := priorityModel()
		m.width, m.height = size[0], size[1]
		m.haveData = true
		m.problem = ProblemNone
		m.snap.Connected = true
		m.snap.Nodes = []protocol.NodeView{{NodeID: "one", Hostname: "desk", Online: true}, {NodeID: "two", Hostname: "build", Online: true}}
		for i := range m.snap.Agents {
			a := &m.snap.Agents[i]
			a.Model = "gpt-6.1-sol"
			a.Profile.Task = "Review the next change"
			if a.Hostname == "" {
				a.Hostname = "desk"
				a.NodeID = "one"
			}
		}
		m.snap.Agents[0].Profile.Name = "demo lead"
		m.snap.Agents[2].Profile.Name = "merger"
		m.snap.Agents[4].Profile.Name = "pinned"
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "video", NodeID: "one", Hostname: "desk", Harness: "claude", Model: "opus", Online: true, Status: "idle", Profile: protocol.Profile{Name: "video", Task: "Combine the recorded demos"}})
		m.asks = askItems([]protocol.InboxItem{{ID: "q", SessionID: "video", Kind: "question", Status: "open", Text: "Combine the recorded demos?"}})
		for i := 0; i < 20; i++ {
			m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: fmt.Sprintf("idle-%02d", i), NodeID: "two", Hostname: "build", Harness: "codex", Online: true, Status: "idle"})
		}
		m.selected = "video"
		m.ensureSelection()
		name := fmt.Sprintf("sessions_priority_%dx%d", size[0], size[1])
		golden(t, name, m.testView())
		m.selected = "kid"
		m.ensureSelection()
		m = keys(m, "z")
		view := m.testView()
		if !strings.Contains(ansi.Strip(view), "+2 (1 busy)") {
			t.Fatalf("summary clipped at %d:\n%s", size[0], view)
		}
		golden(t, name+"_collapsed", view)
	}
}

func TestSessionsPriorityLeadOfflineAndCollapseRefresh(t *testing.T) {
	m := priorityModel()
	m.snap.Agents[0].Online = false
	m.showOffline = true
	m.activeOnly = false
	var shown = map[string]protocol.AgentView{}
	for _, a := range m.snap.Agents {
		shown[a.SessionID] = a
	}
	// The tree keeps the merger under its lead when that lead is offline. The
	// old guess fell back to the caller of an open change.
	if p := m.reportParent(shown["merge"], shown); p != "lead" {
		t.Fatalf("offline lead parent %s", p)
	}
	m = priorityModel()
	m.selected = "kid"
	m.ensureSelection()
	m = keys(m, "z")
	m = refresh(m, &fakeSource{snap: m.snap})
	if indexOfID(ids(m), "kid") >= 0 {
		t.Fatal("collapse lost on refresh")
	}
	if keyAction(contextSessions, "z") != "toggle tree" || keyAction(contextSessions, "Z") != "toggle all trees" {
		t.Fatal("missing bindings")
	}
	help := ansi.Strip(strings.Join(m.helpContent(120), "\n"))
	for _, word := range []string{"collapse or expand the selected tree", "collapse or expand all session trees"} {
		if !strings.Contains(help, word) {
			t.Fatalf("missing help %s", word)
		}
	}
}

func TestSessionRoleTagsDoNotLeakANSI(t *testing.T) {
	m := priorityModel()
	view := ansi.Strip(strings.Join(m.sessionsTable(120, 30), "\n"))
	if strings.Contains(view, "[38;") {
		t.Fatalf("role colour leaked into cell:\n%s", view)
	}
}

func TestSessionsPriorityKeepsQuietCallerOfBusyWorker(t *testing.T) {
	m := priorityModel()
	m.snap.Agents[0].Online = false
	// The caller remains as context even though v has not exposed ended rows.
	// The merger stays under its tree lead, offline or not.
	if got := strings.Join(ids(m), ","); got != "pin,lead,kid,merge" {
		t.Fatalf("caller split from busy worker: %s", got)
	}
	if mk := m.groups()[0].marks["kid"]; mk.root != "lead" || mk.prefix == "" {
		t.Fatal("missing caller tree")
	}
	if mk := groupOf(m, "merge").marks["merge"]; mk.root != "lead" {
		t.Fatalf("merger root %q, want its tree lead", mk.root)
	}
}

func TestSessionsPriorityCollapseSummaryFitsDoubleDigitsAt80(t *testing.T) {
	m := priorityModel()
	m.width, m.height = 80, 24
	for i := 0; i < 10; i++ {
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: fmt.Sprintf("child-%d", i), Harness: "codex", Online: true, Status: "busy", Worker: &protocol.WorkerInfo{Owner: "lead"}})
	}
	m.selected = "lead"
	m.ensureSelection()
	m = keys(m, "z")
	if view := ansi.Strip(m.testView()); !strings.Contains(view, "+12 (11 busy)") {
		t.Fatalf("summary clipped:\n%s", view)
	}
}
