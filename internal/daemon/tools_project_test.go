package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestProjectSkillStaging(t *testing.T) {
	home := t.TempDir()
	checkout := t.TempDir()
	state := t.TempDir()
	src := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(src)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(src)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "projects"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Checkout: checkout, Project: "project", Action: "copy"}}
	r := d.copySkillAt(target, bundle, checkout, ".claude/skills")
	if r.State != "applied" {
		t.Fatalf("result: %+v", r)
	}
	if data, err := os.ReadFile(filepath.Join(checkout, ".claude/skills/demo/SKILL.md")); err != nil || string(data) != "fixture" {
		t.Fatalf("copy: %q %v", data, err)
	}
	if err = os.WriteFile(filepath.Join(checkout, ".claude/skills/demo/SKILL.md"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if r = d.copySkillAt(target, bundle, checkout, ".claude/skills"); r.State != "conflict" {
		t.Fatalf("conflict: %+v", r)
	}
}
