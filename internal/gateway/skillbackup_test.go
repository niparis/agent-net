package gateway

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBackupConcurrentImportIsConsistent(t *testing.T) {
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
	backup := filepath.Join(t.TempDir(), "backup")
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; _, e := repo.put("item", hash, bundle, "node", "location"); errs <- e }()
	go func() { <-start; errs <- g.Backup(backup) }()
	close(start)
	for i := 0; i < 2; i++ {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if err = RestoreBackup(backup, filepath.Join(t.TempDir(), "restored")); err != nil {
		t.Fatalf("inconsistent backup: %v", err)
	}
}

func TestBackupRejectsCorruptLiveStore(t *testing.T) {
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
	hash, _ := inventory.SkillHash(src)
	bundle, _ := inventory.PackSkill(src)
	repo, err := g.skillRepository()
	if err != nil {
		t.Fatal(err)
	}
	v, err := repo.put("fixture", hash, bundle, "node", "path")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := repo.git(nil, "rev-parse", v.Commit+":"+skillPath("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(repo.path, "objects", blob[:2], blob[2:])
	if err = os.Chmod(object, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(object, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "backup")
	if err = g.Backup(dest); err == nil {
		t.Fatal("backup accepted corrupt store")
	}
	if _, err = os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("failed backup left destination: %v", err)
	}
}

func TestRestoreRejectsDestinationInsideBackup(t *testing.T) {
	backup := t.TempDir()
	err := RestoreBackup(backup, filepath.Join(backup, "nested"))
	if err == nil || !strings.Contains(err.Error(), "inside backup") {
		t.Fatalf("unexpected error: %v", err)
	}
}
