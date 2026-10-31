package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolAssignmentRevisions(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "sample", Hash: "abc", State: "ok"}
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","sample","abc"]`
	if _, err = s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES('machine','/repo','demo',0,0)`); err != nil {
		t.Fatal(err)
	}
	a, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"demo"}}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "uninstalled"}, "cto", "test")
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != 1 || b.Revision != 2 || b.Actor != "cto" || b.Assignment.Scope != "uninstalled" {
		t.Fatalf("revisions: %+v %+v", a, b)
	}
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"missing"}}, "owner", "test"); err == nil {
		t.Fatal("accepted unregistered project")
	}
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "agent", "test"); err == nil {
		t.Fatal("accepted agent actor")
	}
}

func TestToolPlanRejectsChangedObservation(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "sample", Hash: "abc", State: "ok", Location: "/fixture"}
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","sample","abc"]`
	plan, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, nil, "owner")
	if err != nil {
		t.Fatal(err)
	}
	item.Location = "/changed"
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyToolPlan(plan.ID, "owner"); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("expected stale plan: %v", err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || len(events) != 2 || events[1].Result != "refused" {
		t.Fatalf("missing refused audit: %+v %v", events, err)
	}
}

func TestToolItemsShowDesiredAndPending(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignTool(`["skill","demo","abc"]`, protocol.ToolAssignment{Scope: "global"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil || len(items) != 1 || items[0].Desired == nil || items[0].Desired.Assignment.Scope != "global" || items[0].Applied != "pending" {
		t.Fatalf("desired: %+v %v", items, err)
	}
}

func TestToolPlanAndApplyAudit(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	targets := []protocol.ToolPlanTarget{{NodeID: "node", Harness: "codex", Destination: "/fixture", Action: "copy", State: "pending"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, targets, "cto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyToolPlan(p.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || len(events) != 2 || events[0].Actor != "cto" || events[1].Actor != "owner" || events[1].Revision != 1 || len(events[1].Targets) != 1 {
		t.Fatalf("history: %+v %v", events, err)
	}
}

func TestGatewayToolPlanRequiresOwnerSession(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"item":"x","target":{"scope":"global"},"actor":"owner"}`)
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "tools.plan", raw); err == nil || !strings.Contains(err.Error(), "owner authority") {
		t.Fatalf("gateway accepted unauthenticated owner: %v", err)
	}
}

func TestApplyReportsStaleWhenItemDisappears(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "global"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = g.applyTool(context.Background(), p.ID, "owner"); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("expected stale plan: %v", err)
	}
}

func TestGatewayToolHistoryRequiresOwnerSession(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "tools.history", []byte(`{"item":"x"}`)); err == nil {
		t.Fatal("gateway accepted unauthenticated history")
	}
}

func TestRejectedToolPlanIsAudited(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "invalid"}}, "owner"); err == nil {
		t.Fatal("invalid plan accepted")
	}
	history, err := g.store.ToolHistory(id)
	if err != nil || len(history) != 1 || history[0].Action != "plan" || history[0].Result != "refused" {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestToolPlanReportsIncompleteScan(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "error"}); err != nil {
		t.Fatal(err)
	}
	plan, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: `["skill","demo","abc"]`, Target: protocol.ToolAssignment{Scope: "uninstalled"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan)
	if !strings.Contains(string(b), "inventory scan incomplete") {
		t.Fatalf("incomplete scan not reported: %s", b)
	}
}

func TestPlanAuditRecordsBaseRevision(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "uninstalled"}, nil, "owner")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || plan.BaseRevision != 1 || len(events) != 1 || events[0].Revision != 1 {
		t.Fatalf("revision missing: %+v %+v %v", plan, events, err)
	}
}

func TestManagedToolRemainsListedWithoutLocations(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "uninstalled"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return false })
	if err != nil || len(items) != 1 || items[0].ID != id || items[0].Desired == nil {
		t.Fatalf("stored item missing: %+v %v", items, err)
	}
}

func TestToolPlanNamesStoredSourceCommit(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetNodeHello(node, "fixture", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "codex"}}); err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	repo, err := g.skillRepository()
	if err != nil {
		t.Fatal(err)
	}
	v, err := repo.put(id, "abc", []byte("fixture"), node, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "global"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.Action == "copy" && strings.Contains(target.Source, v.Commit) && strings.Contains(target.Source, "skills.git") {
			return
		}
	}
	t.Fatalf("source commit missing: %+v", p.Targets)
}

func TestToolPlanIncludesUnseenEnrolledMachines(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: `["skill","demo","abc"]`, Target: protocol.ToolAssignment{Scope: "global"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.NodeID == node && target.Conflict != "" {
			return
		}
	}
	t.Fatalf("missing unseen node: %+v", p.Targets)
}
