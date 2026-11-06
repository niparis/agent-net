package gateway

import (
	"context"
	"github.com/niparis/agent-net/internal/inventory"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolPlanPartialInventoryIsolation(t *testing.T) {
	g, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	good := protocol.InventoryItem{Kind: "skill", Name: "good", Hash: "abc", State: "ok", ScannedAt: now}
	bad := protocol.InventoryItem{Kind: "skill", Name: "bad", Location: "/repo/.agents/skills/bad", Project: "demo", State: "error", Reason: "cannot hash skill safely", ScannedAt: now}
	report := protocol.InventoryReport{State: "error", Reason: inventory.PartialScanReason, ScannedAt: now, Items: []protocol.InventoryItem{good, bad}}
	if err = g.store.SetInventory(node, report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"good", "bad"} {
		hash := "abc"
		if name == "bad" {
			hash = ""
		}
		targets, e := g.toolTargets(context.Background(), `["skill","`+name+`","`+hash+`"]`, protocol.ToolAssignment{Scope: "uninstalled"}, nil)
		if e != nil {
			t.Fatal(e)
		}
		blocked := false
		for _, target := range targets {
			if target.Action == "inspect" && target.Conflict != "" {
				blocked = true
			}
		}
		if blocked != (name == "bad") {
			t.Fatalf("%s: blocked=%v targets=%+v", name, blocked, targets)
		}
	}
}

func TestToolTogglePartialInventoryPlanAndApply(t *testing.T) {
	g, node, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, node, "# Demo\nFixture.\n")
	reports, err := g.store.Inventories()
	if err != nil {
		t.Fatal(err)
	}
	report := reports[0]
	report.ScannedAt = time.Now().UTC()
	report.State = "error"
	report.Reason = inventory.PartialScanReason
	report.Items = append(report.Items, protocol.InventoryItem{Kind: "skill", Name: "bad", State: "error", Location: "/repo/.agents/skills/bad", Reason: "cannot hash skill safely"})
	if err = g.store.SetInventory(node, report); err != nil {
		t.Fatal(err)
	}
	toggleFakeNode(t, g, node, bundle, nil)
	plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) > 0 {
		t.Fatalf("unrelated skill refused: %+v", plan.Conflicts)
	}
	if _, err = g.applyTool(t.Context(), plan.ID, "owner"); err != nil {
		t.Fatal(err)
	}
}

func TestToolPlanScanFailureAndMissingReport(t *testing.T) {
	g, node, id := toggleFixture(t)
	for _, tc := range []struct{ name, reason string }{
		{"scanner", "inventory scanner unavailable"}, {"upload", "inventory upload unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets, err := g.toolTargets(t.Context(), id, protocol.ToolAssignment{Scope: "uninstalled"}, map[string]string{node: tc.reason})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, target := range targets {
				if target.Action == "inspect" && target.Conflict == tc.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("specific failure absent: %+v", targets)
			}
		})
	}
	if _, err := g.store.db.Exec(`DELETE FROM inventory_reports WHERE node_id=?`, node); err != nil {
		t.Fatal(err)
	}
	targets, err := g.toolTargets(t.Context(), id, protocol.ToolAssignment{Scope: "uninstalled"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.Action == "inspect" && strings.Contains(target.Conflict, "no inventory report") {
			return
		}
	}
	t.Fatalf("missing report accepted: %+v", targets)
}

func TestSkillImportFailedObservationNamesCause(t *testing.T) {
	g, node, _ := toggleFixture(t)
	report := protocol.InventoryReport{State: "error", Reason: inventory.PartialScanReason, ScannedAt: time.Now(), Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "good", Hash: "abc", State: "ok"},
		{Kind: "skill", Name: "bad", State: "error", Project: "demo", Location: "/repo/.agents/skills/bad", Reason: "absolute symlink (link)"},
	}}
	if err := g.store.SetInventory(node, report); err != nil {
		t.Fatal(err)
	}
	err := g.ensureSkillImported(t.Context(), `["skill","bad",""]`)
	if err == nil || !strings.Contains(err.Error(), "bad (demo) cannot be read safely: absolute symlink (link)") {
		t.Fatalf("lost failure cause: %v", err)
	}
}
