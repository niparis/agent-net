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
	keep, note, _ := g.mostHeadroom(cands, byID, "opencode")
	if len(keep) != 1 || keep[0].Hostname != "beta" || !strings.Contains(note, "picked beta") || !strings.Contains(note, "alpha") {
		t.Fatalf("keep %+v note %q", keep, note)
	}

	// A machine with no room loses to one that has it, whatever the numbers.
	report("n_b", protocol.WorkerLoad{Running: 12, Queued: 2, Room: protocol.RoomCap, Headroom: 0.9})
	keep, note, _ = g.mostHeadroom(cands, byID, "opencode")
	if len(keep) != 1 || keep[0].Hostname != "alpha" || !strings.Contains(note, "room: cap") {
		t.Fatalf("keep %+v note %q", keep, note)
	}

	// Fewer workers do not beat room: a full machine is never chosen over one with room.
	report("n_a", protocol.WorkerLoad{Running: 3, Queued: 0, Room: protocol.RoomOK, Headroom: 0.10})
	report("n_b", protocol.WorkerLoad{Running: 0, Queued: 0, Room: protocol.RoomCap, Headroom: 0.9})
	keep, note, _ = g.mostHeadroom(cands, byID, "opencode")
	if len(keep) != 1 || keep[0].Hostname != "alpha" || !strings.Contains(note, "room: cap") {
		t.Fatalf("keep %+v note %q", keep, note)
	}
	report("n_b", protocol.WorkerLoad{Running: 1, Queued: 0, Room: protocol.RoomOK, Headroom: 0.40})

	// A machine that does not report (an older agent-net) leaves the old rule.
	delete(g.usage.m, "n_b")
	if keep, _, missing := g.mostHeadroom(cands, byID, "opencode"); keep != nil || !missing {
		t.Errorf("chose with a machine that cannot say: %+v missing %v", keep, missing)
	}
	// A machine that has no headroom figure does the same.
	report("n_b", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: -1})
	if keep, _, missing := g.mostHeadroom(cands, byID, "opencode"); keep != nil || !missing {
		t.Errorf("chose with an unknown headroom: %+v missing %v", keep, missing)
	}
	// A machine without the harness is not a candidate; one machine left is not a choice.
	report("n_b", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.5})
	nodes["n_b"].Harnesses = nil
	if keep, _, missing := g.mostHeadroom(cands, byID, "opencode"); keep != nil || missing {
		t.Errorf("one capable machine is not a choice: %+v missing %v", keep, missing)
	}
}

// Starts handed out since a machine's last load report count against it, so a
// burst spreads. A newer report resets the count, because it shows the workers
// the gateway handed out before it was taken.
func TestStartsHandedOutSinceAReportCountUntilTheNextReport(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	yes := true
	node := func(id string) *protocol.NodeView {
		return &protocol.NodeView{NodeID: id, Online: true, Launch: &yes, Harnesses: []protocol.HarnessInfo{{Name: "opencode"}}}
	}
	nodes := map[string]*protocol.NodeView{"n_a": node("n_a"), "n_b": node("n_b")}
	byID := func(id string) *protocol.NodeView { return nodes[id] }
	cands := []protocol.ProjectView{
		{Name: "shop", NodeID: "n_a", Hostname: "alpha"},
		{Name: "shop", NodeID: "n_b", Hostname: "beta"},
	}
	report := func(id string, wl protocol.WorkerLoad) {
		g.usage.set(id, s.now(), protocol.UsageReport{Workers: &wl})
	}
	report("n_a", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.5})
	report("n_b", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.4})

	// Three starts with no report between them: alpha gets two, beta one.
	var landed []string
	for range 3 {
		keep, _, _ := g.mostHeadroom(cands, byID, "opencode")
		if len(keep) != 1 {
			t.Fatalf("no choice: %+v", keep)
		}
		landed = append(landed, keep[0].Hostname)
		g.usage.handOut(keep[0].NodeID)
	}
	if strings.Join(landed, ",") != "alpha,beta,alpha" {
		t.Fatalf("burst landed %v, want alpha,beta,alpha", landed)
	}

	// A new report with the same figures was taken before those starts, so it
	// shows no workers yet. The count resets, and alpha wins on headroom again.
	report("n_a", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.5})
	report("n_b", protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.4})
	if keep, _, _ := g.mostHeadroom(cands, byID, "opencode"); len(keep) != 1 || keep[0].Hostname != "alpha" {
		t.Fatalf("after a new report the start went to %+v, want alpha", keep)
	}
}

// A start that names no harness can run any harness, so every launcher that
// has one joins the most-room rule. A machine with none cannot run a worker.
func TestMostHeadroomWithoutAHarnessKeepsEveryLauncher(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	yes := true
	node := func(id string) *protocol.NodeView {
		return &protocol.NodeView{NodeID: id, Online: true, Launch: &yes, Harnesses: []protocol.HarnessInfo{{Name: "claude"}}}
	}
	nodes := map[string]*protocol.NodeView{"n_a": node("n_a"), "n_b": node("n_b")}
	byID := func(id string) *protocol.NodeView { return nodes[id] }
	cands := []protocol.ProjectView{
		{Name: "shop", NodeID: "n_a", Hostname: "alpha"},
		{Name: "shop", NodeID: "n_b", Hostname: "beta"},
	}
	g.usage.set("n_a", s.now(), protocol.UsageReport{Workers: &protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.10}})
	g.usage.set("n_b", s.now(), protocol.UsageReport{Workers: &protocol.WorkerLoad{Room: protocol.RoomOK, Headroom: 0.40}})

	keep, note, missing := g.mostHeadroom(cands, byID, "")
	if missing || len(keep) != 1 || keep[0].Hostname != "beta" || !strings.Contains(note, "picked beta") {
		t.Fatalf("keep %+v note %q missing %v", keep, note, missing)
	}

	// A named harness keeps today's rule: only machines that have it.
	if keep, _, _ := g.mostHeadroom(cands, byID, "opencode"); keep != nil {
		t.Errorf("named a harness no machine has: %+v", keep)
	}

	// A launcher with no harness at all cannot run the work.
	nodes["n_a"].Harnesses = nil
	if keep, _, missing := g.mostHeadroom(cands, byID, ""); keep != nil || missing {
		t.Errorf("one capable launcher is not a choice: %+v missing %v", keep, missing)
	}
}
