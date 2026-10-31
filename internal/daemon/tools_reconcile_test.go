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

func TestSkillRootStaysAnchoredAfterParentSwap(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	path, closeRoot, err := openSkillRoot(home, ".claude/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer closeRoot()
	original := filepath.Join(home, ".claude", "skills")
	if err = os.Rename(original, filepath.Join(home, "moved")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, original); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "fixture"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(outside, "fixture")); !os.IsNotExist(err) {
		t.Fatalf("wrote through swapped parent: %v", err)
	}
	if _, err = os.Stat(filepath.Join(home, "moved", "fixture")); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveSkillBackupAndChangedContent(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(fakeHome, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(fakeHome, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(fakeHome, "state"))

	home, state := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(path)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: path, Hash: hash, Source: protocol.InventoryNative}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Harness: "claude", Source: path}}
	if err = os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := d.removeSkill(target); r.State != "conflict" || r.Reason != "changed since plan" {
		t.Fatalf("changed: %+v", r)
	}
	if err = os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("remove: %+v", r)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
	var found bool
	err = filepath.WalkDir(filepath.Join(state, "skill-backups"), func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "SKILL.md" {
			b, e := os.ReadFile(p)
			found = e == nil && string(b) == "original"
		}
		return nil
	})
	if err != nil || !found {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestForgetSkillExcludeRefcount(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(fakeHome, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(fakeHome, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(fakeHome, "state"))

	state := t.TempDir()
	repo := t.TempDir()
	if _, err := projectGit(repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	line := "/.agents/skills/demo/"
	if err := os.WriteFile(exclude, []byte("# existing\n"+line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []skillManifestEntry{{Item: "i", Path: filepath.Join(repo, "a"), Exclude: exclude, ExcludeLine: line, ExcludeOwned: true}, {Item: "i", Path: filepath.Join(repo, "b"), Exclude: exclude, ExcludeLine: line, ExcludeOwned: true}}
	b, _ := json.Marshal(entries)
	if err := os.WriteFile(filepath.Join(state, "skill-manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: state}}
	if err := d.forgetSkill(entries[0].Path); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(exclude)
	if !strings.Contains(string(b), line) {
		t.Fatalf("removed referenced line: %q", b)
	}
	if err := d.forgetSkill(entries[1].Path); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(exclude)
	if string(b) != "# existing\n" {
		t.Fatalf("exclude: %q", b)
	}
}

func TestRemoveSkillRejectsChangedObservationAndIsIdempotent(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(fakeHome, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(fakeHome, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(fakeHome, "state"))

	home, state := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "SKILL.md")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(path)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Harness: "claude", Source: path}}
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, _ := inventory.SkillHash(path)
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: path, Hash: changed, Source: protocol.InventoryNative}}
	if r := d.removeSkill(target); r.State != "conflict" || r.Reason != "changed since plan" {
		t.Fatalf("changed observation: %+v", r)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	d.toolsReport.Items = nil
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("absent retry: %+v", r)
	}
}

func TestRemoveSkillRefusesTrackedGlobalSource(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(fakeHome, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(fakeHome, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(fakeHome, "state"))

	home, state := t.TempDir(), t.TempDir()
	if _, err := projectGit(home, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := projectGit(home, "add", "-f", ".claude/skills/demo/SKILL.md"); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(path)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: path, Hash: hash, Source: protocol.InventoryNative}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Source: path}}
	if r := d.removeSkill(target); r.State != "manual" {
		t.Fatalf("tracked source: %+v", r)
	}
}

func TestRemoveSkillHonorsPlanConflict(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(fakeHome, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(fakeHome, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(fakeHome, "state"))

	home, state := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(path)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: path, Hash: hash, Source: protocol.InventoryNative}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Source: path, Conflict: "source inspection unavailable"}}
	if r := d.removeSkill(target); r.State != "conflict" {
		t.Fatalf("plan conflict ignored: %+v", r)
	}
}

func TestRemoveSkillFromSeparateCodexHome(t *testing.T) {
	home, codex, state := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("AGENT_NET_STATE_DIR", state)
	path := filepath.Join(codex, "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(path)
	d := &Daemon{cfg: Config{InventoryHome: home, CodexHome: codex, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: path, Hash: hash, Source: protocol.InventoryNative}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Source: path}}
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("codex source: %+v", r)
	}
}

func TestRemoveSkillRefusesMixedSystemObservation(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", state)
	path := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(path)
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Location: path, Hash: hash, Source: protocol.InventoryNative}
	sys := obs
	sys.Source = protocol.InventorySystem
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{obs, sys}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Source: path}}
	if r := d.removeSkill(target); r.State != "manual" {
		t.Fatalf("system source: %+v", r)
	}
}

func TestRemoveOwnedExcludeKeepsSimilarLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(home, "state"))
	dir := filepath.Join(home, ".git", "info")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "exclude")
	line := "/.agents/skills/demo/"
	if err := os.WriteFile(path, []byte("# "+line+"\nkeep\n"+line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedExclude(path, line); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "# "+line+"\nkeep\n" {
		t.Fatalf("changed another line: %q", b)
	}
}

func TestRemoveSkillRefusesSymlink(t *testing.T) {
	home, state, outside := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", state)
	path := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "file"), filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: path, Hash: "fixture", Source: protocol.InventoryNative}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: "fixture"}, Target: protocol.ToolPlanTarget{Action: "remove", Source: path}}
	if r := d.removeSkill(target); r.State != "conflict" {
		t.Fatalf("symlink: %+v", r)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "file")); err != nil || string(b) != "external" {
		t.Fatalf("target: %q %v", b, err)
	}
}

func TestRestoreSkillBackupDoesNotReplaceNewSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("AGENT_NET_STATE_DIR", filepath.Join(root, "state"))
	backup, source := filepath.Join(root, "backup"), filepath.Join(root, "source")
	if err := os.Mkdir(backup, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := restoreSkillBackup(backup, source); err == nil {
		t.Fatal("replaced new source")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup lost: %v", err)
	}
}
