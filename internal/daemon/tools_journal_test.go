package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestSkillJournalDurableRecord(t *testing.T) {
	state := t.TempDir()
	j, err := newSkillJournal(state, skillJournalRecord{Operation: "remove", Target: "/fixture/skill", Backup: "/fixture/backup", BeforeHash: "abc", Step: "prepared"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(j.path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("journal directory: %v %v", info, err)
	}
	info, err = os.Stat(j.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal file: %v %v", info, err)
	}
	j.record.Step = "moved"
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(j.path)
	if err != nil {
		t.Fatal(err)
	}
	var got skillJournalRecord
	if err := json.Unmarshal(b, &got); err != nil || got.Step != "moved" || got.BeforeHash != "abc" || got.Target != "/fixture/skill" {
		t.Fatalf("record: %+v %v", got, err)
	}
	if err := j.finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(j.path); !os.IsNotExist(err) {
		t.Fatalf("journal remains: %v", err)
	}
}

func TestSkillJournalCopyStaged(t *testing.T) {
	home, state, source := t.TempDir(), t.TempDir(), t.TempDir()
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
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "fixture", Version: hash, Assignment: protocol.ToolAssignment{Scope: "global"}}, Target: protocol.ToolPlanTarget{Harness: "claude", Action: "copy"}}
	called := false
	skillJournalHook = func(step string) error {
		if step != "staged" {
			return nil
		}
		called = true
		entries, err := os.ReadDir(filepath.Join(state, "skill-journal"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("journal: %v %v", entries, err)
		}
		data, err := os.ReadFile(filepath.Join(state, "skill-journal", entries[0].Name()))
		if err != nil {
			t.Fatal(err)
		}
		var rec skillJournalRecord
		if err = json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Operation != "copy" || rec.Step != "staged" || rec.AfterHash != hash || rec.Target != filepath.Join(home, ".claude", "skills", "demo") {
			t.Fatalf("record: %+v", rec)
		}
		if got, err := inventory.SkillHash(rec.Stage); err != nil || got != hash {
			t.Fatalf("stage: %s %v", got, err)
		}
		return nil
	}
	defer func() { skillJournalHook = nil }()
	if r := d.copyGlobalSkill(target, bundle); r.State != "applied" {
		t.Fatalf("copy: %+v", r)
	}
	if !called {
		t.Fatal("staging hook not called")
	}
	entries, err := os.ReadDir(filepath.Join(state, "skill-journal"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("finished journal: %v %v", entries, err)
	}
}

func TestRemoveSkillJournalAfterBackup(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	source := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(source)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, StateDir: state}}
	d.toolsReport.Items = []protocol.InventoryItem{{Kind: "skill", Name: "demo", Location: source, Hash: hash, Source: protocol.InventoryNative}}
	target := protocol.ToolTarget{Name: "demo", Revision: protocol.ToolRevision{Item: "item", Version: hash}, Target: protocol.ToolPlanTarget{Action: "remove", Source: source}}
	skillJournalHook = func(step string) error {
		if step == "backed_up" {
			return os.ErrInvalid
		}
		return nil
	}
	defer func() { skillJournalHook = nil }()
	if r := d.removeSkill(target); r.State != "failed" {
		t.Fatalf("got %+v", r)
	}
	entries, err := os.ReadDir(filepath.Join(state, "skill-journal"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("journal: %v %v", entries, err)
	}
	b, err := os.ReadFile(filepath.Join(state, "skill-journal", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var rec skillJournalRecord
	if err = json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Operation != "remove" || rec.Step != "backed_up" || rec.Target != source || rec.BeforeHash != hash {
		t.Fatalf("record: %+v", rec)
	}
	if h, e := inventory.SkillHash(rec.Backup); e != nil || h != hash {
		t.Fatalf("backup hash %q: %v", h, e)
	}
}
