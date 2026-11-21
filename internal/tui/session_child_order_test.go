package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The lead and its workers in childOrderFixture. The worker IDs run opposite
// to their start times, so an ID tie-break cannot pass for a start order:
// workerIDs[0] started first and has the highest ID.
const childOrderLead = "s_0000000000000600"

var workerIDs = []string{
	"s_0000000000000605",
	"s_0000000000000604",
	"s_0000000000000603",
	"s_0000000000000602",
	"s_0000000000000601",
}

// childOrderFixture is a lead with five workers under it, oldest start first.
// Every worker is busy on some steps and idle on others.
func childOrderFixture() Snapshot {
	snap := fakeSnapshot()
	snap.Agents = []protocol.AgentView{agent(childOrderLead, "n_mac", "Mac", "claude", "homelab lead", func(a *protocol.AgentView) {
		a.AgentID = "org-homelab"
		a.FirstSeen = t0.Add(-2 * time.Hour).Unix()
	})}
	for i, id := range workerIDs {
		snap.Agents = append(snap.Agents, agent(id, "n_mac", "Mac", "opencode", fmt.Sprintf("worker %d", i), func(a *protocol.AgentView) {
			a.Worker = &protocol.WorkerInfo{ID: fmt.Sprintf("w_child%d", i), Owner: childOrderLead, Project: "homelab",
				StartedAt: t0.Add(time.Duration(i-5) * 10 * time.Minute).Unix(), Task: "task"}
			a.FirstSeen = a.Worker.StartedAt
		}))
	}
	return snap
}

// childOrderStep moves every worker on step n: a busy or idle flip, a new
// activity time and a new report time. A busy worker was active just now; an
// idle one was last active more than the working grace ago, so it is not
// Working. The lead and the start times stay put.
func childOrderStep(snap *Snapshot, n int) {
	for i := range workerIDs {
		a := &snap.Agents[i+1]
		a.LastSeen = t0.Add(time.Duration(n) * time.Second).Unix()
		if (n+i)%2 == 0 {
			a.Status = "busy"
			a.LastActive = t0.Add(time.Duration(n*7+i*3) * time.Second).Unix()
		} else {
			a.Status = "idle"
			a.LastActive = t0.Add(-2*time.Hour - time.Duration(n*7+i*3)*time.Second).Unix()
		}
	}
}

// Workers under one lead keep their start order while they go busy and idle.
// The tree view used to sort siblings by busy first, so a worker that changed
// state moved past its siblings on the next refresh.
func TestWorkersUnderLeadKeepStartOrderWhileTheyChange(t *testing.T) {
	snap := childOrderFixture()
	m := treeModel(120, 34, snap)
	src := &fakeSource{snap: snap, events: fakeEvents()}
	want := append([]string{childOrderLead}, workerIDs...)
	for n := range 12 {
		childOrderStep(&snap, n)
		src.snap = snap
		m = refresh(m, src)
		if got := ids(m); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("step %d: order\n got %v\nwant %v", n, got, want)
		}
	}
}

// The owner's selection follows the session ID, not the row number, while the
// workers under the same lead change state.
func TestSelectedWorkerStaysSelectedWhileSiblingsChange(t *testing.T) {
	snap := childOrderFixture()
	m := treeModel(120, 34, snap)
	src := &fakeSource{snap: snap, events: fakeEvents()}
	m.selectSession(workerIDs[2])
	if m.selected != workerIDs[2] {
		t.Fatalf("select: got %s", m.selected)
	}
	for n := range 12 {
		childOrderStep(&snap, n)
		src.snap = snap
		m = refresh(m, src)
		if m.selected != workerIDs[2] {
			t.Fatalf("step %d: selection moved to %s", n, m.selected)
		}
		if got := m.visible()[m.selIdx].SessionID; got != workerIDs[2] {
			t.Fatalf("step %d: row %d is %s, want the selected worker", n, m.selIdx, got)
		}
	}
}
