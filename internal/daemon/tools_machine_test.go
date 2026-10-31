package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// Two machines share a gateway. The owner plans a skill for one of them only:
// only that machine gets the copy, and undo takes it away again from that
// machine alone.
func TestMachineScopedSkillReachesOnlyThatMachine(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	homeA, homeB := filepath.Join(root, "a"), filepath.Join(root, "b")
	source := filepath.Join(homeA, ".claude", "skills", "demo")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(homeB, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	na := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = homeA; c.CodexHome = filepath.Join(homeA, "codex") }})
	nb := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = homeB; c.CodexHome = filepath.Join(homeB, "codex") }})
	cli := cliClient(t, na)
	var item protocol.ToolItem
	eventually(t, "skill inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill"})
		if err != nil {
			return false
		}
		var page protocol.ToolsPage
		if json.Unmarshal(raw, &page) != nil {
			return false
		}
		for _, v := range page.Items {
			if v.Name == "demo" && len(v.Locations) > 0 {
				item = v
				return true
			}
		}
		return false
	})
	loc := item.Locations[0]
	cli.mustCall("tools_import", protocol.ToolImport{Item: item.ID, Location: protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}})
	raw := cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global", Machines: []string{nb.cfg.NodeID}}})
	var p protocol.ToolPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) == 0 {
		t.Fatal("no targets")
	}
	for _, target := range p.Targets {
		if target.NodeID == na.cfg.NodeID {
			t.Fatalf("plan has a target for the machine that was not chosen: %+v", target)
		}
	}
	cli.mustCall("tools_apply", protocol.ToolPlanID{ID: p.ID})
	installed := filepath.Join(homeB, ".agents", "skills", "demo", "SKILL.md")
	eventually(t, "skill installed on the chosen machine", func() bool {
		data, err := os.ReadFile(installed)
		return err == nil && string(data) == "fixture"
	})
	eventually(t, "results reported", func() bool {
		raw, err := cli.call("tools_item", map[string]any{"id": item.ID})
		if err != nil {
			return false
		}
		var detail protocol.ToolDetail
		return json.Unmarshal(raw, &detail) == nil && detail.Item.Applied == "applied"
	})
	// The machine that was not chosen got nothing new.
	for _, rel := range []string{".agents/skills/demo", ".pi/agent/skills/demo", ".config/opencode/skills/demo"} {
		if _, err := os.Stat(filepath.Join(homeA, rel)); !os.IsNotExist(err) {
			t.Fatalf("machine A got %s: %v", rel, err)
		}
	}
	// Undo reverses the change on the chosen machine only.
	cli.mustCall("tools_undo", protocol.ToolUndoRequest{Item: item.ID})
	eventually(t, "copy removed on the chosen machine", func() bool {
		_, err := os.Stat(installed)
		return os.IsNotExist(err)
	})
	if data, err := os.ReadFile(filepath.Join(source, "SKILL.md")); err != nil || string(data) != "fixture" {
		t.Fatalf("the original on machine A changed: %q %v", data, err)
	}
}

// A daemon ignores a target that names another machine, even if the gateway
// sent it by mistake.
func TestReconcileSkipsTargetsOfAnotherMachine(t *testing.T) {
	d := &Daemon{cfg: Config{NodeID: "n_me"}}
	mine := protocol.ToolTarget{Revision: protocol.ToolRevision{Assignment: protocol.ToolAssignment{Scope: "global", Machines: []string{"n_me"}}}, Target: protocol.ToolPlanTarget{NodeID: "n_me"}}
	other := protocol.ToolTarget{Revision: protocol.ToolRevision{Assignment: protocol.ToolAssignment{Scope: "global", Machines: []string{"n_other"}}}, Target: protocol.ToolPlanTarget{NodeID: "n_me"}}
	elsewhere := protocol.ToolTarget{Revision: protocol.ToolRevision{Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{NodeID: "n_other"}}
	got := d.targetsForThisNode([]protocol.ToolTarget{mine, other, elsewhere})
	if len(got) != 1 || got[0].Target.NodeID != "n_me" || len(got[0].Revision.Assignment.Machines) != 1 {
		t.Fatalf("kept %+v", got)
	}
}
