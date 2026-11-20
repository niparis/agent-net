package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// importablePlanFixture puts one importable native MCP entry on the node and
// returns the candidate key for it.
func importablePlanFixture(t *testing.T) (*Gateway, string) {
	t.Helper()
	g, node := scopeGateway(t)
	ok := &protocol.MCPImportInfo{Class: protocol.MCPImportable, Transport: "stdio", EnvNames: []string{"API_TOKEN"}}
	setNative(t, g, node, nativeEntry("claude", protocol.InventoryUser, "", "Fetch_Tool", "/home/u/.claude.json", ok))
	page := candidatePage(t, g, protocol.MCPImportCandidatesRequest{Class: protocol.MCPImportable})
	if len(page.Candidates) != 1 {
		t.Fatalf("candidates: %+v", page)
	}
	return g, page.Candidates[0].Key
}

func wantPlanNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "plan not found") {
		t.Fatalf("%s: %v, want plan not found", what, err)
	}
}

// The import plan is read back by its id. With the node offline the plan
// carries a conflict, and apply reports that conflict. A live node is needed
// for the apply itself.
func TestImportPlanIsReadByItsIdAndItsConflictsReported(t *testing.T) {
	g, key := importablePlanFixture(t)
	ctx := context.Background()
	plan, err := g.planMCPImport(ctx, key, "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.applyMCPImport(ctx, plan.ID, "owner", "")
	if err == nil || !strings.Contains(err.Error(), "plan has conflicts") {
		t.Fatalf("apply of an offline plan: %v, want plan has conflicts", err)
	}
}

// The native removal plan keeps its state between reads and writes, and a
// stored state refuses the apply.
func TestNativeRemovalPlanStateIsReadAndWritten(t *testing.T) {
	g, key := importablePlanFixture(t)
	ctx := context.Background()
	plan, err := g.planMCPNativeRemove(ctx, key, "owner")
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := g.loadNativePlan(plan.ID)
	if err != nil || p.State != "pending" {
		t.Fatalf("loaded %q: %v", p.State, err)
	}
	p.State = "applied"
	if err = g.saveNativePlan(p); err != nil {
		t.Fatal(err)
	}
	if p, _, err = g.loadNativePlan(plan.ID); err != nil || p.State != "applied" {
		t.Fatalf("saved state %q: %v", p.State, err)
	}
	if _, err = g.applyMCPNativeRemove(ctx, plan.ID, "owner", ""); err == nil || !strings.Contains(err.Error(), "plan already applied") {
		t.Fatalf("apply of an applied plan: %v", err)
	}
}

// A plan id only finds its own kind. Each apply below must say the plan is not
// found, because the plan belongs to another kind.
func TestPlanIdsOnlyFindTheirOwnKind(t *testing.T) {
	g, key := importablePlanFixture(t)
	ctx := context.Background()
	importPlan, err := g.planMCPImport(ctx, key, "owner")
	if err != nil {
		t.Fatal(err)
	}
	nativePlan, err := g.planMCPNativeRemove(ctx, key, "owner")
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	toolPlan, err := g.store.SaveToolPlan(`["skill","demo","abc"]`, protocol.ToolAssignment{Scope: "global"},
		[]protocol.ToolPlanTarget{{NodeID: "node", Harness: "codex", Destination: "/fixture", Action: "copy", State: "pending"}}, "cto")
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.applyMCPImport(ctx, nativePlan.ID, "owner", "")
	wantPlanNotFound(t, "native removal id as an import", err)
	_, err = g.applyMCPImport(ctx, toolPlan.ID, "owner", "")
	wantPlanNotFound(t, "tool plan id as an import", err)
	_, err = g.applyMCPNativeRemove(ctx, importPlan.ID, "owner", "")
	wantPlanNotFound(t, "import id as a native removal", err)
	_, err = g.applyMCPNativeRemove(ctx, toolPlan.ID, "owner", "")
	wantPlanNotFound(t, "tool plan id as a native removal", err)
	if _, err = g.store.ApplyToolPlan(importPlan.ID, "owner"); err == nil {
		t.Fatal("import id applied as a tool plan")
	}
	if _, err = g.store.ApplyToolPlan(toolPlan.ID, "owner"); err != nil {
		t.Fatalf("tool plan no longer applies: %v", err)
	}
}
