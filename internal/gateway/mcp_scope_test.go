package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// scopeGateway returns a gateway with one node, two registered projects and
// one federated server named "files".
func scopeGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	node, _, err := g.store.CreateNode("nas", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, node, "/repo/alpha", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, node, "/repo/beta", "beta"); err != nil {
		t.Fatal(err)
	}
	if err = g.store.AddMCPServer(mcpSpec("files", node)); err != nil {
		t.Fatal(err)
	}
	return g, node
}

func TestFederatedServerKeepsGlobalScopeWithoutAssignment(t *testing.T) {
	g, _ := scopeGateway(t)
	a, err := g.store.MCPAssignment("files")
	if err != nil || a.Scope != "global" {
		t.Fatalf("no row must mean global: %+v %v", a, err)
	}
	srvs, err := g.mcpList()
	if err != nil || len(srvs) != 1 || srvs[0].Scope != "global" {
		t.Fatalf("list scope: %+v %v", srvs, err)
	}
}

func TestFederatedServerAssignedThroughPlanAndApply(t *testing.T) {
	g, node := scopeGateway(t)
	ctx := context.Background()
	id, ok, err := g.store.MCPItemID("files")
	if err != nil || !ok {
		t.Fatalf("no item for the server: %v %v", ok, err)
	}
	plan, err := g.planTool(ctx, protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{"alpha"}}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 || len(plan.Targets) != 1 || plan.Targets[0].Action != "federate" || plan.Targets[0].NodeID != node {
		t.Fatalf("plan: %+v", plan)
	}
	// Nothing changes before the plan is applied.
	if a, _ := g.store.MCPAssignment("files"); a.Scope != "global" {
		t.Fatalf("plan changed the scope: %+v", a)
	}
	res, err := g.applyTool(ctx, plan.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Targets) != 1 || res.Targets[0].State != "ok" {
		t.Fatalf("outcome: %+v", res.Targets)
	}
	a, err := g.store.MCPAssignment("files")
	if err != nil || a.Scope != "projects" || len(a.Projects) != 1 || a.Projects[0] != "alpha" {
		t.Fatalf("assignment: %+v %v", a, err)
	}
	// The plan and the apply are in the audit trail.
	events, err := g.store.ToolHistory(id)
	if err != nil || len(events) != 2 || events[1].Action != "apply" {
		t.Fatalf("audit: %+v %v", events, err)
	}
	// Undo is a new revision that restores the earlier scope.
	if _, err = g.undoTool(ctx, id, "owner"); err != nil {
		t.Fatal(err)
	}
	if a, _ = g.store.MCPAssignment("files"); a.Scope == "projects" {
		t.Fatalf("after undo: %+v", a)
	}
}

func TestFederatedAssignmentNeedsOwnerAndRegisteredProject(t *testing.T) {
	g, _ := scopeGateway(t)
	id, _, _ := g.store.MCPItemID("files")
	if _, err := g.store.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "agent", "test"); err == nil {
		t.Fatal("accepted a non-owner actor")
	}
	if _, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{"nope"}}}, "owner"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered project: %v", err)
	}
}

func TestFederatedServerShowsScopeInToolItems(t *testing.T) {
	g, node := scopeGateway(t)
	id, _, _ := g.store.MCPItemID("files")
	if _, err := g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"beta", "alpha"}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: "mcp"}, func(n string) bool { return n == node })
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v %v", items, err)
	}
	it := items[0]
	if !it.Federated || it.Name != "files" || it.Scope != "projects" || strings.Join(it.ScopeProjects, ",") != "alpha,beta" || it.Host != "nas" || !it.HostOnline || it.Applied != "applied" {
		t.Fatalf("item: %+v", it)
	}
}

func TestFederatedItemSurvivesInventoryAndGoesWithServer(t *testing.T) {
	g, node := scopeGateway(t)
	id, _, _ := g.store.MCPItemID("files")
	// A scan of the machine must not drop the item.
	if err := g.store.SetInventory(node, protocol.InventoryReport{State: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := g.store.MCPItemID("files"); !ok {
		t.Fatal("inventory scan dropped the federated item")
	}
	if _, err := g.store.AssignTool(id, protocol.ToolAssignment{Scope: "uninstalled"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	// Removing the server forgets its assignment: a server added again under
	// the same name starts global, not with the old scope.
	if ok, err := g.store.RemoveMCPServer("files", node, ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := g.store.AddMCPServer(mcpSpec("files", node)); err != nil {
		t.Fatal(err)
	}
	if a, _ := g.store.MCPAssignment("files"); a.Scope != "global" {
		t.Fatalf("old scope came back: %+v", a)
	}
}
