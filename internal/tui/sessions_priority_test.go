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
		{SessionID: "lead", NodeID: "one", Hostname: "desk", Online: true, Status: "idle", Harness: "claude", RepoLeadExplicit: true, Repo: protocol.RepoInfo{Project: "demo"}, Profile: protocol.Profile{Name: "lead"}},
		{SessionID: "kid", NodeID: "two", Hostname: "build", Online: true, Status: "busy", Harness: "codex", Worker: &protocol.WorkerInfo{Owner: "lead"}, Profile: protocol.Profile{Name: "worker"}},
		{SessionID: "merge", Online: true, Status: "idle", Harness: "claude", Type: "merger", Repo: protocol.RepoInfo{Project: "demo"}, Worker: &protocol.WorkerInfo{Owner: "other"}},
		{SessionID: "other", Online: true, Status: "idle", Harness: "claude"},
		{SessionID: "pin", Online: true, Status: "idle", Harness: "claude", PinnedAt: 1},
	}
	m.activeOnly, m.showOffline = true, false
	return m
}

func TestSessionsPriorityGroups(t *testing.T) {
	m := priorityModel()
	g := m.groups()
	if len(g) != 2 || g[0].host != "Working" || g[1].host != "Idle" {
		t.Fatalf("groups: %+v", g)
	}
	if got := strings.Join(ids(m), ","); got != "lead,kid,merge,pin" {
		t.Fatalf("order %s", got)
	}
	m.inboxItems = []protocol.InboxItem{{ID: "q", SessionID: "kid", Kind: "question", Status: "open"}}
	if g = m.groups(); g[0].host != "Needs you" || len(g[0].agents) != 3 {
		t.Fatalf("question did not move whole tree: %+v", g)
	}
	m.inboxItems = nil
	m.snap.Agents[1].Status = "needs you"
	if m.groups()[0].host != "Needs you" {
		t.Fatal("needs you status ignored")
	}
}

func TestSessionsPriorityCollapse(t *testing.T) {
	m := priorityModel()
	m.selected = "kid"
	m.ensureSelection()
	m = keys(m, "z")
	if got := strings.Join(ids(m), ","); got != "lead,pin" {
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
	// Without an explicit lead, the starter owns the merger.
	m.activeOnly = false
	g := m.groups()
	found := false
	for _, group := range g {
		for i, a := range group.agents {
			if a.SessionID == "merge" {
				found = i > 0 && group.agents[i-1].SessionID == "other" && group.marks[a.SessionID].prefix == "└─ "
			}
		}
	}
	if !found {
		t.Fatalf("merger not under starter: %v", ids(m))
	}
	m.snap.Agents[2].Worker = nil
	for _, g := range m.groups() {
		if g.marks["merge"].prefix != "" {
			t.Fatal("orphan merger nested")
		}
	}
}

func TestSessionsPriorityIdleAndInbox(t *testing.T) {
	m := priorityModel()
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "1 more idle · v to show") {
		t.Fatalf("missing fold:\n%s", view)
	}
	m.inboxItems = []protocol.InboxItem{{ID: "q", SessionID: "kid", Kind: "question", Status: "open"}}
	view = ansi.Strip(m.testView())
	if !strings.Contains(view, fmt.Sprintf("Inbox: %d to decide", m.waitingCount())) || strings.Contains(view, "waiting in Inbox") {
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
		m.inboxItems = []protocol.InboxItem{{ID: "q", SessionID: "video", Kind: "question", Status: "open", Text: "Combine the recorded demos?"}}
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
	if p := m.mergerParent(shown["merge"], shown); p != "other" {
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
	if got := strings.Join(ids(m), ","); got != "lead,kid,pin" {
		t.Fatalf("caller split from busy worker: %s", got)
	}
	if m.groups()[0].marks["kid"].prefix != "└─ " {
		t.Fatal("missing caller tree")
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
