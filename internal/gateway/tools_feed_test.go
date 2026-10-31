package gateway

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolTargetsOnlyAppliedForNode(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	targets := []protocol.ToolPlanTarget{{NodeID: "one", Harness: "codex", Action: "copy"}, {NodeID: "two", Harness: "pi", Action: "copy"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, targets, "owner")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ToolTargets("one")
	if err != nil || len(got) != 0 {
		t.Fatalf("unapplied: %+v %v", got, err)
	}
	if _, err = s.ApplyToolPlan(p.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	got, err = s.ToolTargets("one")
	if err != nil || len(got) != 1 || got[0].Target.NodeID != "one" || got[0].Revision.Revision != 1 || got[0].Name != "demo" {
		t.Fatalf("targets: %+v %v", got, err)
	}
}

func TestToolResultsOnlyCurrentOwnedTarget(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, []protocol.ToolPlanTarget{{NodeID: "one", Harness: "codex", Action: "copy"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyToolPlan(p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	result := protocol.ToolResult{Item: id, Revision: r.Revision, Index: 0, State: "applied"}
	if err = s.ReportToolResult("two", result); err == nil {
		t.Fatal("accepted another node's result")
	}
	if err = s.ReportToolResult("one", result); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil || len(items) != 1 || items[0].Applied != "applied" || len(items[0].Results) != 1 {
		t.Fatalf("results: %+v %v", items, err)
	}
	result.State = "other"
	if err = s.ReportToolResult("one", result); err == nil {
		t.Fatal("accepted invalid state")
	}
}

func TestRemovalWaitsForAllCopies(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok", Scope: "user", Location: "/source"}
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = s.db.Exec(`INSERT INTO skill_versions(item,hash,commit_id,node_id,location,imported_at) VALUES(?,?,?,?,?,?)`, id, "abc", "fixture", "one", "fixture", 0); err != nil {
		t.Fatal(err)
	}
	targets := []protocol.ToolPlanTarget{{NodeID: "one", Action: "remove", Source: "/source"}, {NodeID: "two", Action: "copy"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, targets, "owner")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := s.ApplyToolPlan(p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ToolTargets("one")
	if err != nil || len(got) != 1 || got[0].CopiesReady {
		t.Fatalf("before copy: %+v %v", got, err)
	}
	if err = s.ReportToolResult("two", protocol.ToolResult{Item: id, Revision: rev.Revision, Index: 1, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.ToolTargets("one")
	if err != nil || len(got) != 1 || !got[0].CopiesReady {
		t.Fatalf("after copy: %+v %v", got, err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil || len(items) != 1 || items[0].Applied != "partial" {
		t.Fatalf("partial result: %+v %v", items, err)
	}
}

func TestUndoToolIsNewRevisionForAppliedTargetsOnly(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("one", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	targets := []protocol.ToolPlanTarget{{NodeID: "one", Harness: "claude", Action: "copy", Destination: "/d/one"}, {NodeID: "two", Harness: "pi", Action: "copy", Destination: "/d/two"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, targets, "owner")
	if err != nil {
		t.Fatal(err)
	}
	r1, err := s.ApplyToolPlan(p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReportToolResult("one", protocol.ToolResult{Item: id, Revision: r1.Revision, Index: 0, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.UndoTool(id, "agent"); err == nil {
		t.Fatal("accepted a non-owner actor")
	}
	r2, up, err := s.UndoTool(id, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Revision != 2 || r2.Assignment.Scope != "uninstalled" || up.UndoOf != 1 {
		t.Fatalf("undo revision: %+v %+v", r2, up)
	}
	if len(up.Targets) != 1 || up.Targets[0].NodeID != "one" || up.Targets[0].Action != "undo_copy" || up.Targets[0].Destination != "/d/one" {
		t.Fatalf("undo targets: %+v", up.Targets)
	}
	// The never-applied machine has nothing to reverse and receives no work.
	if got, err := s.ToolTargets("two"); err != nil || len(got) != 0 {
		t.Fatalf("unapplied machine: %+v %v", got, err)
	}
	got, err := s.ToolTargets("one")
	if err != nil || len(got) != 1 || got[0].Target.Action != "undo_copy" || got[0].Revision.Revision != 2 {
		t.Fatalf("pending undo: %+v %v", got, err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || events[len(events)-1].Action != "undo" || events[len(events)-1].Actor != "owner" {
		t.Fatalf("audit: %+v %v", events, err)
	}
	// Undoing the undo returns to the first assignment and copies again.
	if err = s.ReportToolResult("one", protocol.ToolResult{Item: id, Revision: 2, Index: 0, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	r3, up2, err := s.UndoTool(id, "cto")
	if err != nil || r3.Revision != 3 || r3.Assignment.Scope != "global" || len(up2.Targets) != 1 || up2.Targets[0].Action != "copy" {
		t.Fatalf("redo: %+v %+v %v", r3, up2, err)
	}
}

func TestToolOutcomesPerTarget(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := `["skill","demo","abc"]`
	r := protocol.ToolRevision{Item: id, Revision: 1}
	targets := []protocol.ToolPlanTarget{
		{NodeID: "a", Machine: "alpha", Action: "copy"},
		{NodeID: "b", Machine: "beta", Action: "copy"},
		{NodeID: "c", Machine: "gamma", Action: "copy"},
		{NodeID: "d", Machine: "delta", Action: "remove"},
		{NodeID: "e", Machine: "eps", Action: "copy"},
	}
	for i, res := range []struct{ node, state, reason string }{{"a", "applied", ""}, {"b", "conflict", "destination differs"}, {"d", "failed", "cannot move source to backup"}} {
		idx := []int{0, 1, 3}[i]
		if _, err = s.db.Exec(`INSERT INTO tool_results(item,revision,target_index,node_id,state,reason) VALUES(?,?,?,?,?,?)`, id, 1, idx, res.node, res.state, res.reason); err != nil {
			t.Fatal(err)
		}
	}
	out, waiting := s.toolOutcomes(r, targets, func(n string) bool { return n == "c" })
	want := []struct{ state, reason string }{{"ok", ""}, {"conflict", "destination differs"}, {"pending", "waiting for the machine to report"}, {"failed", "cannot move source to backup"}, {"pending", "machine offline; applies when it reconnects"}}
	if !waiting || len(out.Targets) != 5 {
		t.Fatalf("outcomes: %+v waiting=%v", out, waiting)
	}
	for i, w := range want {
		if out.Targets[i].State != w.state || out.Targets[i].Reason != w.reason {
			t.Errorf("target %d: %+v want %+v", i, out.Targets[i], w)
		}
	}
	if out.Revision != 1 || out.Targets[0].Machine != "alpha" {
		t.Fatalf("revision fields: %+v", out)
	}
}
