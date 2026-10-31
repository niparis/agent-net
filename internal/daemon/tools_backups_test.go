package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
)

func makeBackup(t *testing.T, d *Daemon, item string, when time.Time, content string) string {
	t.Helper()
	stamp := filepath.Join(d.skillBackupDir(item), when.UTC().Format("20060102T150405.000000000Z")+"-x")
	skill := filepath.Join(stamp, "home", ".claude", "skills", "demo")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func TestPruneSkillBackupsKeepsNewest20AndDropsOld(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	skillBackupNow = func() time.Time { return now }
	defer func() { skillBackupNow = time.Now }()
	old := makeBackup(t, d, "item", now.Add(-31*24*time.Hour), "old")
	var fresh []string
	for i := 0; i < 23; i++ {
		fresh = append(fresh, makeBackup(t, d, "item", now.Add(-time.Duration(i+1)*time.Hour), "new"))
	}
	d.pruneSkillBackups()
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatalf("old backup remains: %v", err)
	}
	entries, _ := os.ReadDir(d.skillBackupDir("item"))
	if len(entries) != 20 {
		t.Fatalf("kept %d", len(entries))
	}
	if _, err := os.Lstat(fresh[0]); err != nil {
		t.Fatalf("newest removed: %v", err)
	}
	if _, err := os.Lstat(fresh[22]); !os.IsNotExist(err) {
		t.Fatalf("21st newest remains: %v", err)
	}
}

func TestPruneKeepsBackupNamedByJournal(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	skillBackupNow = func() time.Time { return now }
	defer func() { skillBackupNow = time.Now }()
	old := makeBackup(t, d, "item", now.Add(-90*24*time.Hour), "old")
	if _, err := newSkillJournal(d.cfg.StateDir, skillJournalRecord{Operation: "remove", Target: "/home/.claude/skills/demo", Backup: filepath.Join(old, "home", ".claude", "skills", "demo"), Step: "backed_up"}); err != nil {
		t.Fatal(err)
	}
	d.pruneSkillBackups()
	if _, err := os.Lstat(old); err != nil {
		t.Fatalf("journaled backup removed: %v", err)
	}
}

func TestHistoryShowsBackupTimeAndSizeNeverContent(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir()}}
	when := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	makeBackup(t, d, "item", when, "TOP-SECRET-TOKEN")
	out := d.withLocalBackups(map[string]any{"events": []any{}}, "item")
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "TOP-SECRET") {
		t.Fatalf("content leaked: %s", b)
	}
	var got struct {
		Backups []struct {
			Time   int64  `json:"time"`
			Size   int64  `json:"size"`
			Source string `json:"source"`
		} `json:"backups"`
	}
	if err = json.Unmarshal(b, &got); err != nil || len(got.Backups) != 1 {
		t.Fatalf("backups: %s %v", b, err)
	}
	if got.Backups[0].Time != when.UnixNano() || got.Backups[0].Size != int64(len("TOP-SECRET-TOKEN")) || got.Backups[0].Source != "/home/.claude/skills/demo" {
		t.Fatalf("backup: %+v", got.Backups[0])
	}
}

func TestBackupIsOwnerOnly(t *testing.T) {
	d, target, state, source, _ := removeFixture(t)
	file := filepath.Join(source, "SKILL.md")
	script := filepath.Join(source, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	h, err := inventory.SkillHash(source)
	if err != nil {
		t.Fatal(err)
	}
	target.Revision.Version = h
	d.toolsReport.Items[0].Hash = h
	if r := d.removeSkill(target); r.State != "applied" {
		t.Fatalf("remove: %+v", r)
	}
	n := 0
	_ = filepath.WalkDir(filepath.Join(state, "skill-backups"), func(p string, e os.DirEntry, err error) error {
		info, _ := e.Info()
		if e.IsDir() && info.Mode().Perm() != 0700 {
			t.Errorf("%s mode %v", p, info.Mode().Perm())
		}
		if !e.IsDir() {
			n++
			if want := os.FileMode(0600); e.Name() == "run.sh" && info.Mode().Perm() != 0700 || e.Name() == "SKILL.md" && info.Mode().Perm() != want {
				t.Errorf("%s mode %v", p, info.Mode().Perm())
			}
		}
		return nil
	})
	if n != 2 {
		t.Fatalf("files: %d", n)
	}
}
