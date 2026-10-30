package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// When a project is on several machines, the one with the most room wins, and
// the note says why. With one machine that cannot say, the old rule stays.
func TestMostHeadroomPicksTheRoomiestMachine(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	yes := true
	node := func(id string) *protocol.NodeView {
		return &protocol.NodeView{NodeID: id, Online: true, Launch: &yes, Harnesses: []protocol.HarnessInfo{{Name: "opencode"}}}
	}
	nodes := map[string]*protocol.NodeView{"n_a": node("n_a"), "n_b": node("n_b"), "n_c": node("n_c")}
	byID := func(id string) *protocol.NodeView { return nodes[id] }
	cands := []protocol.ProjectView{
		{Name: "shop", NodeID: "n_a", Hostname: "alpha"},
		{Name: "shop", NodeID: "n_b", Hostname: "beta"},
	}
	report := func(id string, wl protocol.WorkerLoad) {
		g.usage.set(id, s.now(), protocol.UsageReport{Workers: &wl})
	}
	report("n_a", protocol.WorkerLoad{Running: 3, Queued: 0, Room: protocol.RoomOK, Headroom: 0.10})
	report("n_b", protocol.WorkerLoad{Running: 1, Queued: 0, Room: protocol.RoomOK, Headroom: 0.40})
	keep, note := g.mostHeadroom(cands, byID, "opencode")
	if len(keep) != 1 || keep[0].Hostname != "beta" || !strings.Contains(note, "picked beta") || !strings.Contains(note, "alpha") {
		t.Fatalf("keep %+v note %q", keep, note)
	}

	// A machine with no room loses to one that has it, whatever the numbers.
	report("n_b", protocol.WorkerLoad{Running: 12, Queued: 2, Room: protocol.RoomCap, Headroom: 0.9})
	keep, note = g.mostHeadroom(cands, byID, "opencode")
	if len(keep) != 1 || keep[0].Hostname != "alpha" || !strings.Contains(note, "room: cap") {
		t.Fatalf("keep %+v note %q", keep, note)
	}

	// A machine that does not report (an older agent-net) leaves the old rule.
	delete(g.usage.m, "n_b")
	if keep, _ := g.mostHeadroom(cands, byID, "opencode"); keep != nil {
		t.Errorf("chose with a machine that cannot say: %+v", keep)
	}
	// A machine that has no headroom figure does the same.
	report("n_b", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: -1})
	if keep, _ := g.mostHeadroom(cands, byID, "opencode"); keep != nil {
		t.Errorf("chose with an unknown headroom: %+v", keep)
	}
	// A machine without the harness is not a candidate; one machine left is not a choice.
	report("n_b", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.5})
	nodes["n_b"].Harnesses = nil
	if keep, _ := g.mostHeadroom(cands, byID, "opencode"); keep != nil {
		t.Errorf("one capable machine is not a choice: %+v", keep)
	}
}
