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
			a.RepoLeadExplicit = true
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
		for _, a := range m.visible() {
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
	marks := map[string]treeMark{}
	for _, g := range m.groups() {
		for id, mk := range g.marks {
			marks[id] = mk
		}
	}
	if marks[mergerWorkerID].prefix != "├─ " {
		t.Errorf("worker prefix %q", marks[mergerWorkerID].prefix)
	}
	if marks[mergerID].prefix != "└─ " {
		t.Errorf("merger prefix %q, want the last child glyph", marks[mergerID].prefix)
	}
	// The dim role tag follows the name, with the tree glyph intact.
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
		t.Errorf("merger role tag: %q", row)
	}
}

func TestMergerExplicitLeadWinsDeliveryCaller(t *testing.T) {
	m := treeModel(120, 34, mergerFixture())
	m.delivery.changes = []deliveryChange{{Repo: "agent-net", Caller: secondMainID, State: "open"}}
	shown := map[string]protocol.AgentView{}
	for _, a := range m.snap.Agents {
		shown[a.SessionID] = a
	}
	if got := m.mergerParent(shown[mergerID], shown); got != mainID {
		t.Fatalf("parent %s", got)
	}
}

func TestMergerWithoutShownParentStaysTopLevel(t *testing.T) {
	snap := fakeSnapshot()
	snap.Agents = append(snap.Agents, agent(mergerID, "n_mac", "Mac", "claude", "orphan merger", func(a *protocol.AgentView) {
		a.Type = "merger"
		a.Repo.Project, a.Profile.Project = "no-agent-here", "no-agent-here"
	}))
	m := treeModel(120, 34, snap)
	g := group{}
	for _, band := range m.groups() {
		if indexOfID(agentIDs(band.agents), mergerID) >= 0 {
			g = band
		}
	}
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

func TestMergerFilterKeepsWholeTree(t *testing.T) {
	m := treeModel(120, 34, mergerFixture())
	m.filter = "merger"
	got := ids(m)
	if indexOfID(got, mainID) < 0 || indexOfID(got, mergerID) < 0 || indexOfID(got, mergerWorkerID) < 0 {
		t.Fatalf("filter split tree: %v", got)
	}
}

func TestMergerTreeGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := "tree_merger_" + itoa(size.w) + "x" + itoa(size.h)
		m := quietFixtures(treeModel(size.w, size.h, mergerFixture()))
		m.delivery.changes = []deliveryChange{{ID: "ch_ed5f0000", Repo: "agent-net", Caller: mainID, Step: "test", StepState: protocol.StepStateRunning, OwnerKind: protocol.OwnerCaller, Steps: []deliveryStep{{Name: "test", State: protocol.StepStateRunning, Note: "claimed by " + mainID}}}}
		golden(t, name, selectID(m, mergerID).testView())
	}
}

// An idle merger with no explicit lead still shows under the caller of its
// project's open change, and only the top-level idle agents fold.
func TestMergerIdleWithoutLeadGolden(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		snap := mergerFixture()
		for i := range snap.Agents {
			if snap.Agents[i].SessionID == mainID {
				snap.Agents[i].RepoLeadExplicit = false
			}
		}
		for i := 0; i < 18; i++ {
			snap.Agents = append(snap.Agents, agent("s_00000000000002"+itoa(10+i), "n_vm", "build-vm", "codex", "idle "+itoa(10+i), nil))
		}
		m := quietFixtures(treeModel(size.w, size.h, snap))
		m.activeOnly, m.showOffline = true, false
		m.delivery.changes = []deliveryChange{{ID: "ch_ed5f0000", Repo: "agent-net", State: protocol.ChangeOpen, Caller: mainID, Updated: 200}}
		m = selectID(m, mergerID)
		name := "tree_merger_idle_" + itoa(size.w) + "x" + itoa(size.h)
		golden(t, name, m.testView())
	}
}

func TestSessionTaskShowsRunningDelivery(t *testing.T) {
	m := treeModel(80, 24, mergerFixture())
	m.delivery.changes = []deliveryChange{{ID: "ch_ed5f0000", Repo: "agent-net", Caller: mainID, Step: "test", StepState: protocol.StepStateRunning, OwnerKind: protocol.OwnerCaller, Steps: []deliveryStep{{Name: "test", State: protocol.StepStateRunning, Note: "claimed by " + mainID}}}}
	a, ok := m.agentByID(mainID)
	if !ok {
		t.Fatal("missing session")
	}
	if got := m.sessionTaskText(a); got != "test ch_ed5f" {
		t.Fatalf("task: %q", got)
	}
}

func TestSessionTaskUsesClaimedStep(t *testing.T) {
	m := treeModel(80, 24, mergerFixture())
	v := protocol.ChangeView{Change: protocol.Change{ID: "ch_ed5f0000", Caller: mainID}, Steps: []protocol.StepStatus{{StepRef: protocol.StepRef{Step: protocol.StepTest}, State: protocol.StepStateRunning, Note: "claimed by " + mergerID}}}
	m.delivery.changes = []deliveryChange{deliveryChangeFromView(v, t0)}
	a, _ := m.agentByID(mergerID)
	if got := m.sessionTaskText(a); got != "test ch_ed5f" {
		t.Fatalf("claimed task: %q", got)
	}
}
