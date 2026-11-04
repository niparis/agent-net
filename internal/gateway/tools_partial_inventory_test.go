package gateway

import (
	"context"
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
	report := protocol.InventoryReport{State: "error", Reason: "one or more sources could not be scanned", ScannedAt: now, Items: []protocol.InventoryItem{good, bad}}
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
	report.Reason = "one or more sources could not be scanned"
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
