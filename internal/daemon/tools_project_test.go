package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func projectFixture(t *testing.T) (*Daemon, protocol.ToolTarget, []byte) {
	t.Helper()
	home := t.TempDir()
	checkout := t.TempDir()
	state := t.TempDir()
	source := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
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
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Revision: 1, Version: hash, Assignment: protocol.ToolAssignment{Scope: "projects", Projects: []string{"project"}}}, Target: protocol.ToolPlanTarget{Harness: "claude", Checkout: checkout, Project: "project", Action: "copy"}}
	return d, target, bundle
}
func TestProjectNonGitPending(t *testing.T) {
	d, target, bundle := projectFixture(t)
	r := d.copyProjectSkill(target, bundle)
	if r.State != "pending" || r.Reason != "owner must choose an ignore mechanism" {
		t.Fatalf("result: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(target.Target.Checkout, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("modified checkout: %v", err)
	}
}
func TestProjectCopyExcludesExactPath(t *testing.T) {
	d, target, bundle := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	r := d.copyProjectSkill(target, bundle)
	if r.State != "applied" {
		t.Fatalf("result: %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(checkout, ".git/info/exclude"))
	if err != nil || !strings.Contains(string(b), "/.claude/skills/demo/") {
		t.Fatalf("exclude: %q %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(checkout, ".claude/skills/demo/SKILL.md"))
	if err != nil || string(b) != "fixture" {
		t.Fatalf("copy: %q %v", b, err)
	}
}
func TestProjectTrackedSkillRefused(t *testing.T) {
	d, target, bundle := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	path := filepath.Join(checkout, ".claude/skills/demo/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tracked"), 0600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, checkout, "add", "--", path)
	r := d.copyProjectSkill(target, bundle)
	if r.State != "conflict" {
		t.Fatalf("result: %+v", r)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "tracked" {
		t.Fatalf("overwrote tracked: %q %v", b, err)
	}
}

func TestProjectLinkedWorktreesShareExclude(t *testing.T) {
	d, target, bundle := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q", "-b", "main")
	gitIn(t, checkout, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-qm", "initial")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, checkout, "worktree", "add", "-qb", "other", other)
	if r := d.copyProjectSkill(target, bundle); r.State != "applied" {
		t.Fatalf("first: %+v", r)
	}
	if r := d.copyProjectSkill(target, bundle); r.State != "applied" {
		t.Fatalf("repeat first: %+v", r)
	}
	target.Target.Checkout = other
	if r := d.copyProjectSkill(target, bundle); r.State != "applied" {
		t.Fatalf("second: %+v", r)
	}
	if r := d.copyProjectSkill(target, bundle); r.State != "applied" {
		t.Fatalf("repeat: %+v", r)
	}
	data, err := os.ReadFile(filepath.Join(checkout, ".git/info/exclude"))
	if err != nil || strings.Count(string(data), "/.claude/skills/demo/") != 1 {
		t.Fatalf("exclude: %q %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(d.cfg.StateDir, "skill-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []skillManifestEntry
	if err = json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].ExcludeOwned || !entries[1].ExcludeOwned || entries[0].Exclude != entries[1].Exclude || entries[0].ExcludeLine != entries[1].ExcludeLine {
		t.Fatalf("manifest: %+v", entries)
	}
}

func TestProjectInvalidBundleDoesNotLeaveExclude(t *testing.T) {
	d, target, _ := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	exclude := filepath.Join(checkout, ".git/info/exclude")
	before, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if r := d.copyProjectSkill(target, []byte("bad archive")); r.State == "applied" {
		t.Fatalf("accepted bad bundle: %+v", r)
	}
	after, err := os.ReadFile(exclude)
	if err != nil || string(after) != string(before) {
		t.Fatalf("exclude changed: %q %v", after, err)
	}
}

func TestProjectConflictDoesNotLeaveExclude(t *testing.T) {
	d, target, bundle := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	dest := filepath.Join(checkout, ".claude/skills/demo")
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(checkout, ".git/info/exclude")
	before, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if r := d.copyProjectSkill(target, bundle); r.State != "conflict" {
		t.Fatalf("result: %+v", r)
	}
	after, err := os.ReadFile(exclude)
	if err != nil || string(after) != string(before) {
		t.Fatalf("exclude changed: %q %v", after, err)
	}
}

func TestProjectExcludeSymlinkRefused(t *testing.T) {
	d, target, bundle := projectFixture(t)
	checkout := target.Target.Checkout
	gitIn(t, checkout, "init", "-q")
	exclude := filepath.Join(checkout, ".git/info/exclude")
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(foreign, []byte("/.claude/skills/demo/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(exclude); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, exclude); err != nil {
		t.Fatal(err)
	}
	r := d.copyProjectSkill(target, bundle)
	if r.State == "applied" {
		t.Fatalf("accepted symlink: %+v", r)
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "/.claude/skills/demo/\n" {
		t.Fatalf("changed link target: %q %v", data, err)
	}
}

func TestProjectTargetsReconcileThroughGateway(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	source := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(checkout, 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, checkout, "init", "-q", "-b", "main")
	detect := func(context.Context) []protocol.HarnessInfo {
		return []protocol.HarnessInfo{{Name: "codex"}, {Name: "opencode"}, {Name: "pi"}}
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: detect, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{checkout}})
	var item protocol.ToolItem
	eventually(t, "inventory", func() bool {
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
	loc := item.Locations[0]
	cli.mustCall("tools_import", protocol.ToolImport{Item: item.ID, Location: protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}})
	raw := cli.mustCall("tools_plan", protocol.ToolPlanRequest{Item: item.ID, Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{"project"}}})
	var plan protocol.ToolPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	for _, target := range plan.Targets {
		if target.Action == "copy" && target.Checkout == checkout && target.Destination != filepath.Join(checkout, ".agents/skills/demo") {
			t.Fatalf("not deduped: %+v", target)
		}
	}
	cli.mustCall("tools_apply", protocol.ToolPlanID{ID: plan.ID})
	eventually(t, "project copied", func() bool {
		b, err := os.ReadFile(filepath.Join(checkout, ".agents/skills/demo/SKILL.md"))
		return err == nil && string(b) == "fixture"
	})
	eventually(t, "project results", func() bool {
		raw, err := cli.call("tools_item", map[string]any{"id": item.ID})
		if err != nil {
			return false
		}
		var detail protocol.ToolDetail
		if json.Unmarshal(raw, &detail) != nil {
			return false
		}
		applied := 0
		for _, r := range detail.Item.Results {
			if r.State == "applied" {
				applied++
			}
		}
		return applied >= 3
	})
	for _, rel := range []string{".opencode/skills/demo", ".pi/skills/demo"} {
		if _, err := os.Stat(filepath.Join(checkout, rel)); !os.IsNotExist(err) {
			t.Fatalf("duplicate %s: %v", rel, err)
		}
	}
}

func TestProjectTargetsRefuseShadowingCompatibilityRoot(t *testing.T) {
	checkout := t.TempDir()
	shadow := filepath.Join(checkout, ".opencode/skills/demo")
	if err := os.MkdirAll(shadow, 0700); err != nil {
		t.Fatal(err)
	}
	revision := protocol.ToolRevision{Item: "item", Assignment: protocol.ToolAssignment{Scope: "projects"}}
	targets := []protocol.ToolTarget{{Name: "demo", Revision: revision, Target: protocol.ToolPlanTarget{Checkout: checkout, Harness: "codex", Action: "copy"}}, {Name: "demo", Revision: revision, Target: protocol.ToolPlanTarget{Checkout: checkout, Harness: "opencode", Action: "copy"}}}
	if got := projectTargets(targets)["item\x00"+checkout]; got == "" {
		t.Fatal("accepted shadowing source")
	}
}
func TestProjectTargetsRefuseImpossibleCompatibilityOverlap(t *testing.T) {
	checkout := t.TempDir()
	revision := protocol.ToolRevision{Item: "item", Assignment: protocol.ToolAssignment{Scope: "projects"}}
	var targets []protocol.ToolTarget
	for _, h := range []string{"claude", "codex", "opencode"} {
		targets = append(targets, protocol.ToolTarget{Name: "demo", Revision: revision, Target: protocol.ToolPlanTarget{Checkout: checkout, Harness: h, Action: "copy"}})
	}
	if got := projectTargets(targets)["item\x00"+checkout]; got == "" {
		t.Fatal("accepted incompatible visibility")
	}
}
