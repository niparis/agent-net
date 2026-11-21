package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A backup keeps the archive rows, and a restore opens them again.
func TestBackupAndRestoreKeepTheArchive(t *testing.T) {
	g, _ := archiveFixture(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "demo"), "---\ndescription: Demo.\n---\nbody\n")
	if _, err := g.skillArchivePut(skillContentOf(t, "demo", folder, hash), skillArchiveSource{Kind: "machine", Node: "n", Path: folder}, "owner", false); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if err := g.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backup, "skills.git")); !os.IsNotExist(err) {
		t.Fatalf("backup holds the retired skills.git: %v", err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err := RestoreBackup(backup, target); err != nil {
		t.Fatal(err)
	}
	db, err := OpenStore(filepath.Join(target, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM skill_archive WHERE name='demo' AND hash=?`, hash).Scan(&n); err != nil || n != 1 {
		t.Fatalf("restored archive rows: %d %v", n, err)
	}
}

// A write that runs while the backup is taken leaves a backup that restores.
func TestBackupConcurrentWriteIsConsistent(t *testing.T) {
	g, _ := archiveFixture(t)
	dir := t.TempDir()
	folder, hash := archiveSkillFolder(t, filepath.Join(dir, "demo"), "---\ndescription: Demo.\n---\nbody\n")
	backup := filepath.Join(t.TempDir(), "backup")
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, e := g.skillArchivePut(skillContentOf(t, "demo", folder, hash), skillArchiveSource{Kind: "machine", Node: "n", Path: folder}, "owner", false)
		errs <- e
	}()
	go func() { <-start; errs <- g.Backup(backup) }()
	close(start)
	for i := 0; i < 2; i++ {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if err := RestoreBackup(backup, filepath.Join(t.TempDir(), "restored")); err != nil {
		t.Fatalf("inconsistent backup: %v", err)
	}
}

// A backup whose database is damaged is refused, and no target is left.
func TestRestoreRejectsACorruptDatabase(t *testing.T) {
	g, _ := archiveFixture(t)
	backup := filepath.Join(t.TempDir(), "backup")
	if err := g.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "gateway.db"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	if err := RestoreBackup(backup, bad); err == nil {
		t.Fatal("corrupt database restored")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("failed restore created target: %v", err)
	}
}

func TestRestoreRejectsDestinationInsideBackup(t *testing.T) {
	backup := t.TempDir()
	err := RestoreBackup(backup, filepath.Join(backup, "nested"))
	if err == nil || !strings.Contains(err.Error(), "inside backup") {
		t.Fatalf("unexpected error: %v", err)
	}
}
