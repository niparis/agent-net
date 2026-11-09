package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func bundleOf(t *testing.T, content string) (string, []byte) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := inventory.PackSkill(src)
	if err != nil {
		t.Fatal(err)
	}
	return hash, b
}

func updateReq(from, to string, data []byte, loc string) protocol.ToolUpdateCopyRequest {
	return protocol.ToolUpdateCopyRequest{Name: "demo", Location: loc, FromItem: "item-from", FromHash: from, ToItem: "item-to", ToHash: to, Data: data}
}

func TestUpdateSkillCopyReplacesAnUneditedOlderCopy(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	v1, _ := deployFixture(t, d, "demo", "version one")
	v2, v2bundle := bundleOf(t, "version two")
	loc := filepath.Join(home, ".claude/skills/demo")
	d.toolsReport = scanFixture(d)

	r := d.updateSkillCopy(updateReq(v1, v2, v2bundle, loc))
	if r.State != "applied" {
		t.Fatalf("update: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(loc, "SKILL.md")); string(got) != "version two" {
		t.Fatalf("content %q", got)
	}
	var entries []skillManifestEntry
	raw, _ := os.ReadFile(filepath.Join(state, "skill-manifest.json"))
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) != 1 || entries[0].Hash != v2 || entries[0].Item != "item-to" || !entries[0].Owned {
		t.Fatalf("manifest %s %v", raw, err)
	}
	backups, _ := filepath.Glob(filepath.Join(state, "skill-backups", "*", "*", "*"))
	if len(backups) == 0 {
		t.Fatal("the replaced copy was not backed up")
	}
	// The older assignment that made the copy now reads as out of date, not as
	// a mystery conflict.
	old := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item-old", Revision: 1, Version: v1, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	if r := d.copyGlobalSkill(old, nil); r.State != "conflict" || r.Reason != "replaced by a newer version" {
		t.Fatalf("old assignment: %+v", r)
	}
	// After the update the copy is not changed here.
	if it := findSkill(t, scanFixture(d), "demo"); it.ChangedHere || it.Hash != v2 {
		t.Fatalf("after update: %+v", it)
	}
}

func TestUpdateSkillCopyNeverOverwritesAChangedCopy(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	v1, _ := deployFixture(t, d, "demo", "version one")
	_, v2bundle := bundleOf(t, "version two")
	v2, _ := bundleOf(t, "version two")
	loc := filepath.Join(home, ".claude/skills/demo")
	file := filepath.Join(loc, "SKILL.md")
	if err := os.WriteFile(file, []byte("my edit"), 0600); err != nil {
		t.Fatal(err)
	}
	d.toolsReport = scanFixture(d)
	edited := findSkill(t, d.toolsReport, "demo").Hash

	for _, from := range []string{v1, edited} {
		r := d.updateSkillCopy(updateReq(from, v2, v2bundle, loc))
		if r.State == "applied" {
			t.Fatalf("from %s: overwrote a changed copy: %+v", from[:8], r)
		}
		if got, _ := os.ReadFile(file); string(got) != "my edit" {
			t.Fatalf("from %s: content %q", from[:8], got)
		}
	}
	// Still marked after the refused attempts.
	if it := findSkill(t, scanFixture(d), "demo"); !it.ChangedHere {
		t.Fatal("copy lost its changed mark")
	}
}

func TestUpdateSkillCopyRefusesABadBundleBeforeTouchingTheCopy(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	v1, _ := deployFixture(t, d, "demo", "version one")
	v2, _ := bundleOf(t, "version two")
	loc := filepath.Join(home, ".claude/skills/demo")
	d.toolsReport = scanFixture(d)
	_, other := bundleOf(t, "something else")
	if r := d.updateSkillCopy(updateReq(v1, v2, other, loc)); r.State == "applied" {
		t.Fatalf("bad bundle applied: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(loc, "SKILL.md")); string(got) != "version one" {
		t.Fatalf("old copy not kept: %q", got)
	}
}

func TestUpdateSkillCopyRestoresTheOldCopyWhenInstallFails(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	v1, _ := deployFixture(t, d, "demo", "version one")
	v2, v2bundle := bundleOf(t, "version two")
	loc := filepath.Join(home, ".claude/skills/demo")
	d.toolsReport = scanFixture(d)
	// Another harness folder with the same skill name makes the install
	// refuse (it could shadow the new copy), after the old copy was moved.
	if err := os.MkdirAll(filepath.Join(home, ".codex/skills/demo"), 0700); err != nil {
		t.Fatal(err)
	}
	r := d.updateSkillCopy(updateReq(v1, v2, v2bundle, loc))
	if r.State == "applied" || strings.Contains(r.Reason, "could not be restored") {
		t.Fatalf("result: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(loc, "SKILL.md")); string(got) != "version one" {
		t.Fatalf("old copy not restored: %q (%+v)", got, r)
	}
	if e, ok := d.skillManifestEntryAt(loc); !ok || e.Hash != v1 {
		t.Fatalf("manifest after restore: %+v %v", e, ok)
	}
}

func TestUpdateSkillCopyReplacesAProjectCopyAndKeepsItIgnored(t *testing.T) {
	home, state, checkout := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	if out, err := exec.Command("git", "-C", checkout, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state, Hostname: "fixture"}}
	v1, v1bundle := bundleOf(t, "version one")
	deploy := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item-one", Revision: 1, Version: v1, Assignment: protocol.ToolAssignment{Scope: "projects", Projects: []string{"proj"}}}, Target: protocol.ToolPlanTarget{Harness: "claude", Checkout: checkout, Project: "proj", Action: "copy"}}
	if r := d.copyProjectSkill(deploy, v1bundle); r.State != "applied" {
		t.Fatalf("deploy: %+v", r)
	}
	scan := func() protocol.InventoryReport {
		report := inventory.Scan(inventory.Options{Home: home, Machine: "fixture", Projects: []protocol.ProjectEntry{{Name: "proj", Path: checkout}}})
		d.markChangedSkills(&report)
		return report
	}
	d.toolsReport = scan()
	loc := filepath.Join(checkout, ".claude/skills/demo")
	v2, v2bundle := bundleOf(t, "version two")
	r := d.updateSkillCopy(updateReq(v1, v2, v2bundle, loc))
	if r.State != "applied" {
		t.Fatalf("update: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(loc, "SKILL.md")); string(got) != "version two" {
		t.Fatalf("content %q", got)
	}
	out, err := exec.Command("git", "-C", checkout, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("project copy shows in git status: %q %v", out, err)
	}
}
