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
