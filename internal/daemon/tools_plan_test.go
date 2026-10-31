package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
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
}
