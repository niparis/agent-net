package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func undoTargetOf(t protocol.ToolTarget, action string) protocol.ToolTarget {
	t.Target.Action = action
	return t
}

func TestUndoRemoveRestoresFromBackup(t *testing.T) {
	d, target, state, source, hash := removeFixture(t)
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("remove: %+v", r)
	}
	if r := d.undoRemoveSkill(undoTargetOf(target, "undo_remove")); r.State != "applied" {
		t.Fatalf("undo: %+v", r)
	}
	if !skillHashIs(source, hash) {
		t.Fatal("skill was not restored")
	}
	if d.findSkillBackup("item", source, hash) == "" {
		t.Fatal("backup must stay for another undo")
	}
	if journalCount(t, state) != 0 {
		t.Fatal("journal remains")
	}
	// A second undo finds the same content and reports it applied.
	if r := d.undoRemoveSkill(undoTargetOf(target, "undo_remove")); r.State != "applied" {
		t.Fatalf("repeat: %+v", r)
	}
}

func TestUndoRemoveStopsWhenChangedSinceApply(t *testing.T) {
	d, target, _, source, _ := removeFixture(t)
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("remove: %+v", r)
	}
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("later user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	r := d.undoRemoveSkill(undoTargetOf(target, "undo_remove"))
	if r.State != "conflict" || r.Reason != "changed since apply" {
		t.Fatalf("undo: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(source, "SKILL.md")); string(b) != "later user edit" {
		t.Fatalf("user edit lost: %q", b)
	}
}

func installedCopy(t *testing.T) (*Daemon, protocol.ToolTarget, string, string) {
	t.Helper()
	home, state := t.TempDir(), t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(src)
	bundle, _ := inventory.PackSkill(src)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	if r := d.copyGlobalSkill(target, bundle); r.State != "applied" {
		t.Fatalf("copy: %+v", r)
	}
	return d, undoTargetOf(target, "undo_copy"), filepath.Join(home, ".claude", "skills", "demo"), hash
}

func TestUndoCopyRemovesOwnedCopyAndKeepsBackup(t *testing.T) {
	d, target, dest, hash := installedCopy(t)
	if r := d.undoCopySkill(target, nil); r.State != "applied" {
		t.Fatalf("undo: %+v", r)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("copy remains: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json")); strings.Contains(string(b), dest) {
		t.Fatalf("manifest: %s", b)
	}
	if d.findSkillBackup("fixture", dest, hash) == "" {
		t.Fatal("no backup of the removed copy")
	}
}

func TestUndoCopyKeepsEditedCopy(t *testing.T) {
	d, target, dest, _ := installedCopy(t)
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	r := d.undoCopySkill(target, nil)
	if r.State != "conflict" || r.Reason != "changed since apply" {
		t.Fatalf("undo: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "SKILL.md")); string(b) != "edited" {
		t.Fatalf("edit lost: %q", b)
	}
}

func TestUndoCopyReleasesOwnedProjectExclude(t *testing.T) {
	d, target, bundle := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	if r := d.copyProjectSkill(target, bundle); r.State != "applied" {
		t.Fatalf("copy: %+v", r)
	}
	exclude := filepath.Join(checkout, ".git/info/exclude")
	if b, _ := os.ReadFile(exclude); !strings.Contains(string(b), "/.claude/skills/demo/") {
		t.Fatalf("exclude missing: %q", b)
	}
	if r := d.undoCopySkill(undoTargetOf(target, "undo_copy"), nil); r.State != "applied" {
		t.Fatalf("undo: %+v", r)
	}
	if b, _ := os.ReadFile(exclude); strings.Contains(string(b), "/.claude/skills/demo/") {
		t.Fatalf("owned exclude line remains: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(checkout, ".claude/skills/demo")); !os.IsNotExist(err) {
		t.Fatalf("copy remains: %v", err)
	}
}

func TestToolUndoThroughGatewayRemovesInstalledCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
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
	raw = cli.mustCall("tools_apply", protocol.ToolPlanID{ID: p.ID})
	var applied protocol.ToolApplyResult
	if err := json.Unmarshal(raw, &applied); err != nil || applied.Revision != 1 || len(applied.Targets) != len(p.Targets) {
		t.Fatalf("apply answer: %s %v", raw, err)
	}
	for _, o := range applied.Targets {
		if o.State != "ok" {
			t.Errorf("target %+v is not ok in the apply answer", o)
		}
	}
	installed := filepath.Join(home, ".agents", "skills", "demo")
	eventually(t, "skill installed", func() bool {
		data, err := os.ReadFile(filepath.Join(installed, "SKILL.md"))
		return err == nil && string(data) == "fixture"
	})
	eventually(t, "install results reported", func() bool {
		raw, err := cli.call("tools_item", map[string]any{"id": item.ID})
		var detail protocol.ToolDetail
		return err == nil && json.Unmarshal(raw, &detail) == nil && detail.Item.Applied == "applied" && len(detail.Item.Results) == 4
	})
	raw = cli.mustCall("tools_undo", protocol.ToolUndoRequest{Item: item.ID})
	var r protocol.ToolRevision
	if err := json.Unmarshal(raw, &r); err != nil || r.Revision != 2 {
		t.Fatalf("undo revision: %s %v", raw, err)
	}
	eventually(t, "copy removed by undo", func() bool {
		_, err := os.Lstat(installed)
		return os.IsNotExist(err)
	})
	if _, err := os.Lstat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("original skill must stay: %v", err)
	}
}
