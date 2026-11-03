package daemon

import (
	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillRootHasNoDescriptorPaths(t *testing.T) {
	for _, name := range []string{"skill_root_unix.go", "skill_root.go"} {
		data, err := os.ReadFile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []string{"/dev/fd", "/proc/self/fd"} {
			if strings.Contains(string(data), prefix) {
				t.Errorf("%s uses nonportable descriptor path %s", name, prefix)
			}
		}
	}
}

func TestSkillRootCopyRemoveAfterParentSwap(t *testing.T) {
	home, state, src, hash := recoverFixture(t)
	bundle, err := inventory.PackSkill(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	outside := t.TempDir()
	original := filepath.Join(home, ".claude", "skills")
	moved := filepath.Join(home, "moved-skills")
	skillJournalHook = func(step string) error {
		if step != "staged" {
			return nil
		}
		if err := os.Rename(original, moved); err != nil {
			return err
		}
		return os.Symlink(outside, original)
	}
	t.Cleanup(func() { skillJournalHook = nil })
	if r := d.copyGlobalSkill(target, bundle); r.State != "applied" {
		t.Fatalf("copy: %+v", r)
	}
	skillJournalHook = nil
	if _, err := os.Lstat(filepath.Join(outside, "demo")); !os.IsNotExist(err) {
		t.Fatalf("copy escaped: %v", err)
	}
	installed := filepath.Join(moved, "demo")
	if got, err := inventory.SkillHash(installed); err != nil || got != hash {
		t.Fatalf("copy hash: %s %v", got, err)
	}
	// A replaced parent is refused for removal, leaving the outside tree intact.
	target.Target = protocol.ToolPlanTarget{Action: "remove", Source: filepath.Join(original, "demo")}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: target.Target.Source, Hash: hash, Source: protocol.InventoryNative}}
	if r := d.removeSkill(target); r.State != "conflict" {
		t.Fatalf("symlink removal: %+v", r)
	}
	// Restore the real parent and exercise removal and backup through roots.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, original); err != nil {
		t.Fatal(err)
	}
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("remove: %+v", r)
	}
	if backup := d.findSkillBackup("fixture", target.Target.Source, hash); backup == "" {
		t.Fatal("missing verified backup")
	}
}

func TestSkillRootRejectsSymlinkEscape(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	if root, err := makeSkillRoot(base, "escape/skills"); err == nil {
		root.Close()
		t.Fatal("accepted symlink root")
	}
	root, err := makeSkillRoot(base, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("escape/index.lock", []byte("x"), 0600); err == nil {
		t.Fatal("write escaped root")
	}
	if err := renameSkillAt(skillPath{root, "escape/source"}, skillPath{root, "dest"}, false); err == nil {
		t.Fatal("rename escaped root")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside changed: %v %v", entries, err)
	}
}

func TestSkillRootIndexLockExclusiveAndTokenChecked(t *testing.T) {
	base, state := t.TempDir(), t.TempDir()
	root, err := makeSkillRoot(base, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock := skillPath{root, "index.lock"}
	j, err := newSkillJournal(state, skillJournalRecord{Operation: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	defer j.finish()
	release, busy, err := j.acquireIndexLock(filepath.Join(base, "index.lock"), lock)
	if err != nil || busy {
		t.Fatalf("lock: %v %v", busy, err)
	}
	if _, busy, err := j.acquireIndexLock(filepath.Join(base, "index.lock"), lock); err == nil || !busy {
		t.Fatalf("exclusive lock: %v %v", busy, err)
	}
	if err := root.WriteFile("index.lock", []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	if b, err := root.ReadFile("index.lock"); err != nil || string(b) != "foreign" {
		t.Fatalf("foreign lock removed: %s %v", b, err)
	}
	if !removeOwnedIndexLockAt(lock, "foreign") {
		t.Fatal("owned lock remains")
	}
	if err := root.Symlink("../outside", "index.lock"); err != nil {
		t.Fatal(err)
	}
	if removeOwnedIndexLockAt(lock, "foreign") {
		t.Fatal("removed symlink lock")
	}
}
