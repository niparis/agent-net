package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The session IDs of layoutFixture. The numbers set the order of equal rows:
// the idle backtest session has a lower ID than the busy research session, so
// a test can see the busy one come first.
const (
	layoutCTO       = "s_0000000000000501"
	layoutChief     = "s_0000000000000502"
	layoutStrategy  = "s_0000000000000503" // lead of the strategy Agent, busy
	layoutBacktest  = "s_0000000000000504" // member of strategy, idle
	layoutResearch  = "s_0000000000000505" // member of strategy, busy
	layoutWorker    = "s_0000000000000506" // worker that strategy started, busy
	layoutTenebrae  = "s_0000000000000507" // lead of the tenebrae Agent, idle
	layoutTenebrae2 = "s_0000000000000508" // member of tenebrae, busy
	layoutMerger    = "s_0000000000000509" // merger of tenebrae, idle
	layoutTenebrae3 = "s_0000000000000510" // member of tenebrae, idle
)

// layoutFixture is the Sessions list the owner watches: the CTO with the chief
// of staff and two leads under it, busy and idle members, a merger and a worker.
func layoutFixture() Snapshot {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{
		agent(layoutCTO, "n_mac", "Mac", "cto", "CTO", func(a *protocol.AgentView) {
			a.AgentID = protocol.CTONodeName
		}),
		agent(layoutChief, "n_mac", "Mac", "claude", "chief of staff", func(a *protocol.AgentView) {
			a.AgentID = "chief-of-staff"
		}),
		agent(layoutStrategy, "n_mac", "Mac", "claude", "strategy lead", func(a *protocol.AgentView) {
			a.AgentID, a.RepoLeadExplicit, a.ReportsTo = "org-strategy", true, protocol.CTONodeName
			a.Status = "busy"
		}),
		agent(layoutBacktest, "n_mac", "Mac", "claude", "backtest", func(a *protocol.AgentView) {
			a.AgentID, a.ReportsTo = "org-backtest", "org-strategy"
		}),
		agent(layoutResearch, "n_mac", "Mac", "claude", "research", func(a *protocol.AgentView) {
			a.AgentID, a.ReportsTo = "org-research", "org-strategy"
			a.Status = "busy"
		}),
		agent(layoutWorker, "n_mac", "Mac", "opencode", "strategy worker", func(a *protocol.AgentView) {
			a.Status = "busy"
			a.Worker = &protocol.WorkerInfo{ID: "w_layout", Owner: layoutStrategy, Project: "strategies", StartedAt: t0.Unix(), Task: "probe"}
		}),
		agent(layoutTenebrae, "n_mac", "Mac", "claude", "tenebrae lead", func(a *protocol.AgentView) {
			a.AgentID, a.RepoLeadExplicit, a.ReportsTo = "org-tenebrae", true, protocol.CTONodeName
		}),
		agent(layoutTenebrae2, "n_mac", "Mac", "claude", "tenebrae 2", func(a *protocol.AgentView) {
			a.AgentID, a.ReportsTo = "org-tenebrae-2", "org-tenebrae"
			a.Status = "busy"
		}),
		agent(layoutMerger, "n_mac", "Mac", "claude", "tenebrae merger", func(a *protocol.AgentView) {
			a.Type, a.AgentID, a.ReportsTo = "merger", "org-tenebrae-merger", "org-tenebrae"
		}),
		agent(layoutTenebrae3, "n_mac", "Mac", "claude", "tenebrae 3", func(a *protocol.AgentView) {
			a.AgentID, a.ReportsTo = "org-tenebrae-3", "org-tenebrae"
		}),
	}
	return snap
}

// layoutModel shows every session, with the chief of staff loaded. The active
// view folds idle plain sessions, which would hide the rows these tests check.
func layoutModel(w, h int, snap Snapshot) Model {
	m := treeModel(w, h, snap)
	m.activeOnly = false
	m.chief = protocol.ChiefStatus{SessionID: layoutChief, Online: true}
	m.chiefLoaded = true
	return quietFixtures(m)
}

// layoutGroupOf returns the Working or Idle group of the Groups view.
func layoutGroupOf(m Model, host string) group {
	for _, g := range m.groups() {
		if g.host == host {
			return g
		}
	}
	return group{}
}

// The tree view is one group: every session of a tree stays under its root,
// and each set of siblings lists its busy rows first.
func TestTreeViewIsOneGroupAndKeepsTreesWhole(t *testing.T) {
	m := layoutModel(120, 34, layoutFixture())
	groups := m.groups()
	if len(groups) != 1 || !groups[0].tree {
		t.Fatalf("tree view has %d groups, want one tree group", len(groups))
	}
	want := []string{layoutChief, layoutCTO, layoutStrategy, layoutResearch, layoutWorker, layoutBacktest,
		layoutTenebrae, layoutTenebrae2, layoutTenebrae3, layoutMerger}
	if got := ids(m); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tree order\n got %v\nwant %v", got, want)
	}
}

// The tree header counts every session and the busy ones.
func TestTreeViewHeaderCountsSessionsAndBusy(t *testing.T) {
	m := layoutModel(120, 34, layoutFixture())
	if got := m.groupLine(m.groups()[0], 120); !strings.Contains(got, "10 sessions · 4 busy") {
		t.Fatalf("tree header %q, want the session and busy counts", ansiStrip(got))
	}
}

// The Groups view puts each row in the group of its own status, and nests only
// the workers and the mergers, each under its parent when both are in the same
// group.
func TestGroupsViewPutsEachRowInItsOwnGroup(t *testing.T) {
	m := keys(layoutModel(120, 34, layoutFixture()), "G")
	if !m.groupedSessions {
		t.Fatal("t did not switch to the groups view")
	}
	working := layoutGroupOf(m, "Working")
	idle := layoutGroupOf(m, "Idle")
	if got, want := agentIDs(working.agents), []string{layoutStrategy, layoutWorker, layoutResearch, layoutTenebrae2}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Working rows %v, want %v", got, want)
	}
	if got, want := agentIDs(idle.agents), []string{layoutChief, layoutCTO, layoutBacktest, layoutTenebrae, layoutMerger, layoutTenebrae3}; len(got) != len(want) {
		t.Errorf("Idle rows %v, want %v", got, want)
	}
	if mk := working.marks[layoutWorker]; mk.prefix == "" {
		t.Errorf("worker is not nested under its owner: %+v", mk)
	}
	if mk := idle.marks[layoutMerger]; mk.prefix == "" {
		t.Errorf("merger is not nested under its lead in the same group: %+v", mk)
	}
	for _, id := range []string{layoutResearch, layoutCTO, layoutTenebrae2} {
		g := working
		if id == layoutCTO {
			g = idle
		}
		if mk := g.marks[id]; mk.prefix != "" {
			t.Errorf("%s is a plain session in the groups view but has mark %+v", id, mk)
		}
	}
}

// A worker whose owner is in the other group stays a top-level row of its own
// group, marked as a worker with no shown owner.
func TestGroupsViewChildOfOtherGroupIsTopLevel(t *testing.T) {
	snap := layoutFixture()
	for i := range snap.Agents {
		if snap.Agents[i].SessionID == layoutStrategy {
			snap.Agents[i].Status = "idle"
		}
	}
	m := keys(layoutModel(120, 34, snap), "G")
	working := layoutGroupOf(m, "Working")
	if mk, ok := working.marks[layoutWorker]; !ok || mk.prefix != "↳ " {
		t.Fatalf("worker of an idle owner is not a top-level worker row: %+v (in group %v)", mk, agentIDs(working.agents))
	}
	if indexOfID(agentIDs(working.agents), layoutWorker) < 0 {
		t.Fatal("worker is not a Working row")
	}
}

// In the tree view a row that needs the owner comes before a pinned idle row,
// as the Needs you group used to put it first.
func TestTreeViewPutsNeedsYouBeforePinnedIdle(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{
		agent("s_0000000000000601", "n_mac", "Mac", "claude", "pinned idle", func(a *protocol.AgentView) {
			a.PinnedAt = 1
		}),
		agent("s_0000000000000602", "n_mac", "Mac", "claude", "asks", func(a *protocol.AgentView) {}),
	}
	m := layoutModel(120, 34, snap)
	m.asks = askItems([]protocol.InboxItem{{ID: "q", SessionID: "s_0000000000000602", Kind: "question", Status: "open"}})
	if got := strings.Join(ids(m), ","); got != "s_0000000000000602,s_0000000000000601" {
		t.Fatalf("tree order %s, want the row that needs the owner first", got)
	}
}

// One key switches the two views, and the key line names the other view.
func TestSessionsKeyTogglesGroupsAndTree(t *testing.T) {
	m := layoutModel(120, 34, layoutFixture())
	if hasLayoutHint(m.hints(), "G", "groups") == false {
		t.Fatalf("tree view key line has no t groups hint: %v", m.hints())
	}
	m = keys(m, "G")
	if hasLayoutHint(m.hints(), "G", "tree") == false {
		t.Fatalf("groups view key line has no t tree hint: %v", m.hints())
	}
	m = keys(m, "G")
	if m.groupedSessions {
		t.Fatal("second t did not return to the tree view")
	}
}

func hasLayoutHint(hs []hint, key, label string) bool {
	for _, h := range hs {
		if h.key == key && h.label == label {
			return true
		}
	}
	return false
}

// The Sessions list in its two views, at both sizes the owner's screens use.
func TestSessionsTreeGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := layoutModel(size.w, size.h, layoutFixture())
		golden(t, fmt.Sprintf("sessions_tree_%dx%d", size.w, size.h), m.testView())
	}
}

func TestSessionsGroupsGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m := keys(layoutModel(size.w, size.h, layoutFixture()), "G")
		golden(t, fmt.Sprintf("sessions_groups_%dx%d", size.w, size.h), m.testView())
	}
}
