package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNormalizeAssignmentMachines(t *testing.T) {
	a, err := normalizeAssignment(protocol.ToolAssignment{Scope: "global", Machines: []string{"n_b", "n_a"}})
	if err != nil || len(a.Machines) != 2 || a.Machines[0] != "n_a" {
		t.Fatalf("sorted machines: %+v %v", a, err)
	}
	a, err = normalizeAssignment(protocol.ToolAssignment{Scope: "uninstalled", Machines: []string{"n_a"}})
	if err != nil || len(a.Machines) != 1 {
		t.Fatalf("uninstalled on one machine: %+v %v", a, err)
	}
	a, err = normalizeAssignment(protocol.ToolAssignment{Scope: "projects", Projects: []string{"p"}, Machines: []string{"n_a"}})
	if err != nil || len(a.Machines) != 1 {
		t.Fatalf("projects on one machine: %+v %v", a, err)
	}
	for _, bad := range [][]string{{""}, {"n_a", "n_a"}, {" n_a"}} {
		if _, err = normalizeAssignment(protocol.ToolAssignment{Scope: "global", Machines: bad}); err == nil {
			t.Errorf("accepted machines %q", bad)
		}
	}
	a, err = normalizeAssignment(protocol.ToolAssignment{Scope: "global"})
	if err != nil || len(a.Machines) != 0 {
		t.Fatalf("default is every machine: %+v %v", a, err)
	}
}

func machineStore(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok", Scope: "user", Location: "/x"}
	for _, n := range []string{a, b} {
		if err = s.SetInventory(n, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
			t.Fatal(err)
		}
	}
	return s, a, b, `["skill","demo","abc"]`
}

func TestMachineScopedPlanApplyReachesOnlyThatMachine(t *testing.T) {
	s, a, b, id := machineStore(t)
	targets := []protocol.ToolPlanTarget{{NodeID: b, Machine: "mac", Harness: "claude", Action: "copy", Destination: "/d/b"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global", Machines: []string{b}}, targets, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Assignment.Machines) != 1 || p.Assignment.Machines[0] != b {
		t.Fatalf("plan assignment %+v", p.Assignment)
	}
	r, err := s.ApplyToolPlan(p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ToolTargets(a); err != nil || len(got) != 0 {
		t.Fatalf("the other machine got work: %+v %v", got, err)
	}
	got, err := s.ToolTargets(b)
	if err != nil || len(got) != 1 || len(got[0].Revision.Assignment.Machines) != 1 {
		t.Fatalf("scoped machine: %+v %v", got, err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil || len(items) != 1 || items[0].Desired == nil || len(items[0].Desired.Assignment.Machines) != 1 {
		t.Fatalf("desired keeps the machines: %+v %v", items, err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || len(events) != 2 {
		t.Fatalf("history %+v %v", events, err)
	}
	for _, e := range events {
		if e.Assignment == nil || len(e.Assignment.Machines) != 1 || e.Assignment.Machines[0] != b {
			t.Errorf("audit event %s lacks the machine scope: %+v", e.Action, e.Assignment)
		}
	}
	// Undo of the machine-scoped change reverses only that machine.
	if err = s.ReportToolResult(b, protocol.ToolResult{Item: id, Revision: r.Revision, Index: 0, State: "applied"}); err != nil {
		t.Fatal(err)
	}
	r2, up, err := s.UndoTool(id, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Revision != 2 || len(up.Targets) != 1 || up.Targets[0].NodeID != b || up.Targets[0].Action != "undo_copy" {
		t.Fatalf("undo: %+v %+v", r2, up)
	}
	if got, err := s.ToolTargets(a); err != nil || len(got) != 0 {
		t.Fatalf("undo reached the other machine: %+v %v", got, err)
	}
	events, _ = s.ToolHistory(id)
	last := events[len(events)-1]
	if last.Action != "undo" || len(last.Targets) != 1 || last.Targets[0].NodeID != b {
		t.Fatalf("undo audit: %+v", last)
	}
}

func TestAssignRejectsUnknownMachine(t *testing.T) {
	s, _, _, id := machineStore(t)
	if _, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "global", Machines: []string{"n_nobody"}}, "owner", "test"); err == nil || !strings.Contains(err.Error(), "machine") {
		t.Fatalf("accepted an unknown machine: %v", err)
	}
}

func TestMachineScopedRemovalKeepsLastCopyCheckHonest(t *testing.T) {
	s, a, b, id := machineStore(t)
	// The skill is not imported. Removing the copy on one machine is fine
	// because the other machine keeps its copy.
	targets := []protocol.ToolPlanTarget{{NodeID: a, Machine: "research", Source: "/x", Action: "remove", State: "pending"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "uninstalled", Machines: []string{a}}, targets, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyToolPlan(p.ID, "owner"); err != nil {
		t.Fatalf("removal on one machine refused: %v", err)
	}
	// Removing it on every machine is still refused.
	targets = []protocol.ToolPlanTarget{{NodeID: a, Source: "/x", Action: "remove"}, {NodeID: b, Source: "/x", Action: "remove"}}
	p, err = s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "uninstalled"}, targets, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyToolPlan(p.ID, "owner"); err == nil || !strings.Contains(err.Error(), "last copy") {
		t.Fatalf("expected the last copy refusal: %v", err)
	}
}

func TestGatewayPlansForChosenMachineOnly(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a, _, err := g.store.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := g.store.CreateNode("mac", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`UPDATE nodes SET hostname='research-box' WHERE id=?`, a); err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok", Scope: "user", Location: "/x"}
	for _, n := range []string{a, b} {
		if err = g.store.SetInventory(n, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
			t.Fatal(err)
		}
	}
	id := `["skill","demo","abc"]`
	// A machine can be named by its name or its ID.
	for _, who := range []string{a, "research-box"} {
		p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "global", Machines: []string{who}}}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Assignment.Machines) != 1 || p.Assignment.Machines[0] != a {
			t.Fatalf("machine %q resolved to %+v", who, p.Assignment.Machines)
		}
		if len(p.Targets) == 0 {
			t.Fatal("no targets")
		}
		for _, target := range p.Targets {
			if target.NodeID == b {
				t.Fatalf("target for the other machine: %+v", target)
			}
		}
	}
	// Uninstall on one machine verifies only that machine.
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "uninstalled", Machines: []string{b}}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.NodeID == a {
			t.Fatalf("uninstall plan touches the other machine: %+v", target)
		}
	}
	if _, err = g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "global", Machines: []string{"no-such-machine"}}}, "owner"); err == nil || !strings.Contains(err.Error(), "machine") {
		t.Fatalf("unknown machine: %v", err)
	}
}
