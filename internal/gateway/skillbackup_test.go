package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
)

func TestSkillBackupRestoreAndCorruption(t *testing.T) {
	data := t.TempDir()
	g, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	src := t.TempDir()
	if err = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("fixture"), 0600); err != nil {
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
	repo, err := g.skillRepository()
	if err != nil {
		t.Fatal(err)
	}
	v, err := repo.put("fixture", hash, bundle, "node", "path")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err = g.Backup(backup); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err = RestoreBackup(backup, target); err != nil {
		t.Fatal(err)
	}
	db, err := OpenStore(filepath.Join(target, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := openSkillStore(target, db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.read("fixture", hash)
	db.Close()
	if err != nil || len(got) == 0 {
		t.Fatalf("restored: %v", err)
	}
	blob, err := repo.git(nil, "rev-parse", v.Commit+":"+skillPath("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(backup, "skills.git", "objects", blob[:2], blob[2:])
	if err = os.WriteFile(object, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	if err = RestoreBackup(backup, bad); err == nil {
		t.Fatal("corrupt store restored")
	}
	if _, err = os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("failed restore created target: %v", err)
	}
}
