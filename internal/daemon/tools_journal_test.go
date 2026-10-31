package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
