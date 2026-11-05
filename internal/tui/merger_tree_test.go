package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A merger session hangs under its project's main agent, after its workers,
// so the owner can see that the two belong together.

const (
	mainID         = "s_0000000000000101"
	secondMainID   = "s_0000000000000102"
	mergerID       = "s_0000000000000103"
	mergerWorkerID = "s_0000000000000104"
)

func mergerFixture() Snapshot {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents,
		agent(mainID, "n_mac", "Mac", "claude", "agent-net", func(a *protocol.AgentView) {
			a.LastActive, a.LastSeen = t0.Add(-1*time.Minute).Unix(), t0.Add(-1*time.Minute).Unix()
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/agent-net", Worktree: "/Users/n/code/agent-net", Project: "agent-net", Branch: "main"}
			a.Profile.Project = "agent-net"
		}),
		agent(mergerWorkerID, "n_mac", "Mac", "opencode", "fix-login", func(a *protocol.AgentView) {
			a.Mode, a.Model = protocol.ModeBackground, "anthropic/claude-haiku-4-5"
			a.Repo.Project, a.Profile.Project = "agent-net", "agent-net"
			a.Worker = &protocol.WorkerInfo{ID: "w_fix", Owner: mainID, Project: "agent-net",
				StartedAt: t0.Add(-5 * time.Minute).Unix(), Task: "fix-login"}
		}),
		agent(mergerID, "n_mac", "Mac", "claude", "agent-net merger", func(a *protocol.AgentView) {
			a.Type = "merger"
			a.LastActive, a.LastSeen = t0.Add(-2*time.Minute).Unix(), t0.Add(-2*time.Minute).Unix()
			a.Repo = protocol.RepoInfo{Root: "/Users/n/code/agent-net", Project: "agent-net", Branch: "main"}
			a.Profile.Project = "agent-net"
		}))
	return snap
}

func TestMergerHangsUnderProjectAgent(t *testing.T) {
	m := treeModel(120, 34, mergerFixture())
	names := func() []string {
		var out []string
		for _, a := range m.groups()[0].agents {
			out = append(out, a.Profile.Name)
		}
		return out
	}
	order := names()
	at := indexOfID(order, "agent-net")
	if at < 0 {
		t.Fatalf("no agent-net row: %v", order)
	}
	if got := order[at+1]; got != "fix-login" {
		t.Fatalf("after agent-net: %q in %v", got, order)
	}
	if got := order[at+2]; got != "agent-net merger" {
		t.Fatalf("merger is not after the worker: %q in %v", got, order)
	}
	marks := m.groups()[0].marks
	if marks[mergerWorkerID].prefix != "├─ " {
		t.Errorf("worker prefix %q", marks[mergerWorkerID].prefix)
	}
	if marks[mergerID].prefix != "└─ " {
		t.Errorf("merger prefix %q, want the last child glyph", marks[mergerID].prefix)
	}
	// The TYPE column still says merger, and the row hangs with its parent.
	var row string
	for _, l := range lines(selectID(m, mergerID).testView()) {
		if strings.Contains(l, "agent-net merger") {
			row = l
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(row), " "), "└─ agent-net merger") {
		t.Errorf("merger row has no tree glyph: %q", row)
	}
	if !strings.Contains(row, " merger ") || !strings.Contains(row, "merger") {
		t.Errorf("merger TYPE column: %q", row)
	}
}

func TestMergerParentIsDeliveryCaller(t *testing.T) {
	// Two agent sessions of the project. The older one is the caller of the
	// open change, so the merger hangs under it.
	snap := mergerFixture()
	snap.Agents = append(snap.Agents, agent(secondMainID, "n_mac", "Mac", "claude", "agent-net-old", func(a *protocol.AgentView) {
		a.LastActive, a.LastSeen = t0.Add(-20*time.Minute).Unix(), t0.Add(-20*time.Minute).Unix()
		a.Repo.Project, a.Profile.Project = "agent-net", "agent-net"
	}))
	m := treeModel(120, 34, snap)
	m.delivery.changes = []deliveryChange{
		{ID: "ch_old", Repo: "agent-net", State: "open", Caller: secondMainID, Waiting: time.Minute},
		{ID: "ch_new", Repo: "agent-net", State: "open", Caller: mainID, Waiting: 2 * time.Minute},
		{ID: "ch_other", Repo: "tenebrae", State: "open", Caller: mainID, Waiting: time.Second},
	}
	g := m.groups()[0]
	var order []string
	for _, a := range g.agents {
		order = append(order, a.SessionID)
	}
	p := indexOfID(order, secondMainID)
	if p < 0 || p+1 >= len(order) || order[p+1] != mergerID {
		t.Fatalf("merger not under the delivery caller: %v", order)
	}
	if g.marks[mergerID].prefix != "└─ " {
		t.Errorf("merger prefix %q", g.marks[mergerID].prefix)
	}
	if got := len(g.marks[mainID].prefix) + len(g.marks[secondMainID].prefix); got != 0 {
		t.Errorf("a main agent has a mark: %+v", g.marks)
	}
}

func TestMergerParentFallsBackToStableID(t *testing.T) {
	snap := mergerFixture()
	snap.Agents = append(snap.Agents, agent(secondMainID, "n_mac", "Mac", "claude", "agent-net-old", func(a *protocol.AgentView) {
		a.LastActive, a.LastSeen = t0.Unix(), t0.Unix()
		a.Repo.Project, a.Profile.Project = "agent-net", "agent-net"
	}))
	m := treeModel(120, 34, snap) // no delivery data loaded
	g := m.groups()[0]
	var order []string
	for _, a := range g.agents {
		order = append(order, a.SessionID)
	}
	p := indexOfID(order, mainID)
	if p < 0 || p+2 >= len(order) || order[p+2] != mergerID {
		t.Fatalf("merger not under the stable parent: %v", order)
	}
}

func TestMergerWithoutShownParentStaysTopLevel(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent(mergerID, "n_mac", "Mac", "claude", "orphan merger", func(a *protocol.AgentView) {
		a.Type = "merger"
		a.Repo.Project, a.Profile.Project = "no-agent-here", "no-agent-here"
	}))
	m := treeModel(120, 34, snap)
	g := m.groups()[0]
	if mk, ok := g.marks[mergerID]; ok && mk.prefix != "" {
		t.Errorf("lone merger got a mark: %+v", mk)
	}
	found := false
	for _, a := range g.agents {
		if a.SessionID == mergerID {
			found = true
		}
	}
	if !found {
		t.Fatal("lone merger is not a row")
	}
}

func TestMergerFilterHidesParentStaysTopLevel(t *testing.T) {
	m := treeModel(120, 34, mergerFixture())
	m.filter = "merger"
	m.ensureSelection()
	var g *group
	all := m.groups()
	for i := range all {
		if all[i].nodeID == "n_mac" {
			g = &all[i]
		}
	}
	if g == nil {
		t.Fatal("no Mac group")
	}
	if len(g.agents) != 1 || g.agents[0].SessionID != mergerID {
		t.Fatalf("filtered rows: %+v", g.agents)
	}
	if mk, ok := g.marks[mergerID]; ok && mk.prefix != "" {
		t.Errorf("a merger whose parent the filter hides got a mark: %+v", mk)
	}
}

func TestMergerTreeGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := "tree_merger_" + itoa(size.w) + "x" + itoa(size.h)
		m := treeModel(size.w, size.h, mergerFixture())
		golden(t, name, selectID(m, mergerID).testView())
	}
}
