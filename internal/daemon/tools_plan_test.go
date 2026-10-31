package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolPlanDestinationAndCollision(t *testing.T) {
	home := t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home}}
	req := protocol.ToolPlanProbe{Name: "demo", Harness: "claude"}
	got := d.probeToolDestination(req)
	want := filepath.Join(home, ".claude/skills/demo")
	if got.Destination != want || got.Conflict != "" {
		t.Fatalf("destination: %+v", got)
	}
	if err := os.MkdirAll(want, 0700); err != nil {
		t.Fatal(err)
	}
	got = d.probeToolDestination(req)
	if got.Conflict == "" {
		t.Fatal("existing foreign destination not detected")
	}
}

func TestToolsPlanAndApplyOwnerGate(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill"})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	req := protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global"}}
	raw, err := cli.call("tools_plan", req)
	if err != nil {
		t.Fatal(err)
	}
	var plan protocol.ToolPlan
	if err = json.Unmarshal(raw, &plan); err != nil || plan.ID == "" {
		t.Fatalf("plan: %s %v", raw, err)
	}
	pi, _ := helloPi(t, n, "agent-plan", "/work", true)
	if _, err = pi.call("tools_plan", req); err == nil {
		t.Fatal("agent planned assignment")
	}
	if _, err = pi.call("tools_apply", protocol.ToolPlanID{ID: plan.ID}); err == nil {
		t.Fatal("agent applied assignment")
	}
	if _, err = cli.call("tools_apply", protocol.ToolPlanID{ID: plan.ID}); err != nil {
		t.Fatal(err)
	}
	raw, err = cli.call("tools_history", protocol.ToolHistoryRequest{Item: item.ID})
	if err != nil || !strings.Contains(string(raw), `"action":"apply"`) {
		t.Fatalf("history: %s %v", raw, err)
	}
	if _, err = pi.call("tools_history", protocol.ToolHistoryRequest{Item: item.ID}); err == nil {
		t.Fatal("agent read history")
	}
}

func TestCTOToolPlanAuditAndNeeds(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	if err := g.gw.Store().AddArchitect("architect", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetCharter("architect", "needs", "Need a review skill", "owner"); err != nil {
		t.Fatal(err)
	}
	list, err := n.d.runCTOToolRaw(context.Background(), "tools_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list)
	if !strings.Contains(string(b), "Need a review skill") {
		t.Fatalf("needs missing: %s", b)
	}
	req, _ := json.Marshal(protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global"}})
	value, err := n.d.runCTOToolRaw(context.Background(), "tools_plan", req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(value)
	var p protocol.ToolPlan
	if json.Unmarshal(b, &p) != nil || p.Actor != "cto" {
		t.Fatalf("CTO actor: %s", b)
	}
	apply, _ := json.Marshal(protocol.ToolPlanID{ID: p.ID})
	if _, err = n.d.runCTOToolRaw(context.Background(), "tools_apply", apply); err != nil {
		t.Fatal(err)
	}
	history, err := g.gw.Store().ToolHistory(item.ID)
	if err != nil || len(history) != 2 || history[1].Actor != "cto" {
		t.Fatalf("CTO audit: %+v %v", history, err)
	}
}
