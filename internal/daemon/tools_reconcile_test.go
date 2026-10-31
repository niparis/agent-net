package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestCopyGlobalSkillAndRefuseChanges(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(source)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(source)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	result := d.copyGlobalSkill(target, bundle)
	if result.State != "applied" {
		t.Fatalf("copy: %+v", result)
	}
	dest := filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")
	if got, err := os.ReadFile(dest); err != nil || string(got) != "fixture" {
		t.Fatalf("content: %q %v", got, err)
	}
	info, err := os.Stat(filepath.Join(state, "skill-manifest.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("manifest: %v %v", info, err)
	}
	if err = os.WriteFile(dest, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := d.copyGlobalSkill(target, bundle); r.State != "conflict" {
		t.Fatalf("changed destination: %+v", r)
	}
}

func TestCopyGlobalSkillRejectsSymlinkParentAndBadBundle(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	outside := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(source)
	bundle, _ := inventory.PackSkill(source)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	if err := os.Symlink(outside, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if r := d.copyGlobalSkill(target, bundle); r.State != "conflict" {
		t.Fatalf("symlink: %+v", r)
	}
	if err := os.Remove(filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	target.Target.Conflict = "shadowing source"
	if r := d.copyGlobalSkill(target, bundle); r.State != "conflict" {
		t.Fatalf("plan conflict: %+v", r)
	}
	target.Target.Conflict = ""
	target.Revision.Version = "wrong"
	if r := d.copyGlobalSkill(target, bundle); r.State != "failed" {
		t.Fatalf("bad hash: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("bad bundle installed: %v", err)
	}
}

func TestGlobalSkillReconcilesThroughGateway(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
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
			if v.Name == "demo" {
				item = v
				return true
			}
		}
		return false
	})
	loc := item.Locations[0]
	cli.mustCall("tools_import", protocol.ToolImport{Item: item.ID, Location: protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}})
	raw := cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "global"}})
	var p protocol.ToolPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	cli.mustCall("tools_apply", protocol.ToolPlanID{ID: p.ID})
	eventually(t, "skill installed by nudge", func() bool {
		data, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"))
		return err == nil && string(data) == "fixture"
	})
	eventually(t, "reported results", func() bool {
		raw, err := cli.call("tools_item", map[string]any{"id": item.ID})
		if err != nil {
			return false
		}
		var detail protocol.ToolDetail
		if json.Unmarshal(raw, &detail) != nil {
			return false
		}
		return detail.Item.Applied == "applied" && len(detail.Item.Results) == 4
	})
}
