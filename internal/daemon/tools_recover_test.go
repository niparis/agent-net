package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func journalCount(t *testing.T, state string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(state, "skill-journal"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func recoverFixture(t *testing.T) (home, state, source, hash string) {
	t.Helper()
	home, state = t.TempDir(), t.TempDir()
	source = filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := inventory.SkillHash(source)
	if err != nil {
		t.Fatal(err)
	}
	return home, state, source, h
}

// crashAt makes the journal hook fail at a step, which leaves the record
// behind like a daemon that died there.
func crashAt(step string) func() {
	skillJournalHook = func(s string) error {
		if s == step {
			return os.ErrClosed
		}
		return nil
	}
	return func() { skillJournalHook = nil }
}

func removeCrash(t *testing.T, step string) (state, source, hash string, target protocol.ToolTarget) {
	t.Helper()
	home, state, source, hash := recoverFixture(t)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: source, Hash: hash, Source: protocol.InventoryNative}}
	target = protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Source: source}}
	if err := d.recordSkill(target, source, true); err != nil {
		t.Fatal(err)
	}
	defer crashAt(step)()
	if r := d.removeSkill(target); r.State != "failed" {
		t.Fatalf("crash: %+v", r)
	}
	return state, source, hash, target
}

func TestRecoverDeletesStagedCopy(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(src)
	bundle, _ := inventory.PackSkill(src)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	stop := crashAt("staged")
	if r := d.copyGlobalSkill(target, bundle); r.State != "failed" {
		t.Fatalf("crash: %+v", r)
	}
	stop()
	root := filepath.Join(home, ".claude", "skills")
	if entries, _ := os.ReadDir(root); len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), skillStagePrefix) {
		t.Fatalf("stage missing: %v", entries)
	}
	fresh := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	if c := fresh.recoverSkillJournal(); len(c) != 0 {
		t.Fatalf("conflicts: %v", c)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("stage remains: %v", entries)
	}
	if journalCount(t, state) != 0 {
		t.Fatal("journal remains")
	}
}

func TestRecoverFinishesForgetAfterBackup(t *testing.T) {
	state, source, hash, _ := removeCrash(t, "backed_up")
	fresh := &Daemon{cfg: Config{StateDir: state}}
	if c := fresh.recoverSkillJournal(); len(c) != 0 {
		t.Fatalf("conflicts: %v", c)
	}
	b, err := os.ReadFile(filepath.Join(state, "skill-manifest.json"))
	if err != nil || strings.Contains(string(b), source) {
		t.Fatalf("manifest still lists source: %s %v", b, err)
	}
	if journalCount(t, state) != 0 {
		t.Fatal("journal remains")
	}
	found := false
	_ = filepath.WalkDir(filepath.Join(state, "skill-backups"), func(p string, e os.DirEntry, err error) error {
		if err == nil && e.IsDir() && e.Name() == "demo" && skillHashIs(p, hash) {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("backup was removed")
	}
}

func TestRecoverLeavesChangedBackupAsConflict(t *testing.T) {
	state, source, _, target := removeCrash(t, "backed_up")
	rec := readOnlyJournal(t, state)
	if err := os.WriteFile(filepath.Join(rec.Backup, "SKILL.md"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := &Daemon{cfg: Config{StateDir: state}}
	c := fresh.recoverSkillJournal()
	if len(c) != 1 || !strings.Contains(c[0], source) {
		t.Fatalf("conflicts: %v", c)
	}
	if b, _ := os.ReadFile(filepath.Join(rec.Backup, "SKILL.md")); string(b) != "edited" {
		t.Fatalf("backup touched: %q", b)
	}
	if journalCount(t, state) != 1 {
		t.Fatal("journal must stay for review")
	}
	if r := fresh.removeSkill(target); r.State != "conflict" || !strings.Contains(r.Reason, "interrupted") {
		t.Fatalf("tool result: %+v", r)
	}
}

func TestRecoverLeavesChangedInstallAsConflict(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := inventory.SkillHash(src)
	bundle, _ := inventory.PackSkill(src)
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	stop := crashAt("installed")
	if r := d.copyGlobalSkill(target, bundle); r.State != "failed" {
		t.Fatalf("crash: %+v", r)
	}
	stop()
	dest := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	if c := fresh.recoverSkillJournal(); len(c) != 1 {
		t.Fatalf("conflicts: %v", c)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "SKILL.md")); string(b) != "edited" {
		t.Fatalf("destination touched: %q", b)
	}
	if r := fresh.copyGlobalSkill(target, bundle); r.State != "conflict" {
		t.Fatalf("tool result: %+v", r)
	}
}

func lockRecord(t *testing.T, state, lock, token string) {
	t.Helper()
	j, err := newSkillJournal(state, skillJournalRecord{Operation: "remove", Target: "/fixture/skill", BeforeHash: "x", Lock: lock, LockToken: token, Step: "locked"})
	if err != nil {
		t.Fatal(err)
	}
	_ = j
}

func TestRecoverRemovesOwnIndexLock(t *testing.T) {
	state := t.TempDir()
	lock := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lock, []byte("our-token"), 0600); err != nil {
		t.Fatal(err)
	}
	lockRecord(t, state, lock, "our-token")
	(&Daemon{cfg: Config{StateDir: state}}).recoverSkillJournal()
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("own lock remains: %v", err)
	}
}

func TestRecoverKeepsForeignIndexLock(t *testing.T) {
	state := t.TempDir()
	lock := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lock, []byte("someone else"), 0600); err != nil {
		t.Fatal(err)
	}
	lockRecord(t, state, lock, "our-token")
	(&Daemon{cfg: Config{StateDir: state}}).recoverSkillJournal()
	if b, err := os.ReadFile(lock); err != nil || string(b) != "someone else" {
		t.Fatalf("foreign lock: %q %v", b, err)
	}
}
