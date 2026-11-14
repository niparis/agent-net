package gateway

import (
	"bytes"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rewindGatewayStore makes the store in dataDir look like an older release left
// it, so the next New must migrate it. It returns the old schema version.
func rewindGatewayStore(t *testing.T, dataDir string) int {
	t.Helper()
	s, err := OpenStore(filepath.Join(dataDir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := migrationVersion(t, "ALTER TABLE inbox_items ADD COLUMN options") - 1
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version > ?`, old); err != nil {
		t.Fatal(err)
	}
	prepareOldSessionIdentity(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return old
}

// snapshotSchemaVersion reads the version of a database file without opening it
// through the store, which would migrate it.
func snapshotSchemaVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

// seedGatewayParts adds the CA, the server key and the skill repository to a
// data directory, as the first gateway start does. A test that rewinds a store
// and opens it through the gateway needs them for the automatic upgrade backup.
func seedGatewayParts(t *testing.T, dir string, s *Store) {
	t.Helper()
	if _, err := loadOrCreateCA(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateServerKey(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := openSkillStore(dir, s); err != nil {
		t.Fatal(err)
	}
}

// newGatewayDataDir starts a gateway once so the folder holds a database, a
// skill repository and the CA like a real machine.
func newGatewayDataDir(t *testing.T, root string) string {
	t.Helper()
	dataDir := filepath.Join(root, "gateway")
	g, err := New(Options{DataDir: dataDir, Version: "v0.5.61"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestGatewayUpgradeWritesBackupBeforeMigrations(t *testing.T) {
	root := t.TempDir()
	dataDir := newGatewayDataDir(t, root)
	old := rewindGatewayStore(t, dataDir)

	var logs bytes.Buffer
	g, err := New(Options{DataDir: dataDir, Version: "v0.5.62", Logger: log.New(&logs, "", 0)})
	if err != nil {
		t.Fatalf("gateway did not open: %v", err)
	}
	defer g.Close()

	backupsDir := filepath.Join(root, "gateway-backups")
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("upgrade wrote no backup: %v", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("backup folder: %v", entries)
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "pre-v0.5.62-") {
		t.Fatalf("backup folder = %q, want the new version and a UTC time", name)
	}
	dir := filepath.Join(backupsDir, name)
	for _, file := range []string{"gateway.db", "ca.pem", "ca.key", "server-key.pem"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("backup is missing %s: %v", file, err)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "skills.git")); err != nil || !info.IsDir() {
		t.Errorf("backup is missing the skill repository: %v", err)
	}
	if got := snapshotSchemaVersion(t, filepath.Join(dir, "gateway.db")); got != old {
		t.Fatalf("backup schema = %d, want the old %d: the snapshot was taken after the migration", got, old)
	}
	if got, err := g.store.SchemaVersion(); err != nil || got != len(migrations) {
		t.Fatalf("live schema = %d, %v; want %d", got, err, len(migrations))
	}
	line := logs.String()
	if !strings.Contains(line, "event=gateway.backup.before_upgrade") || !strings.Contains(line, dir) || !strings.Contains(line, "size=") {
		t.Fatalf("log line does not name the folder and its size: %q", line)
	}
}

func TestGatewayOpenWithoutPendingMigrationsWritesNoBackup(t *testing.T) {
	root := t.TempDir()
	dataDir := newGatewayDataDir(t, root)
	g, err := New(Options{DataDir: dataDir, Version: "v0.5.61"})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if entries, err := os.ReadDir(filepath.Join(root, "gateway-backups")); err == nil {
		t.Fatalf("a store with no pending migrations wrote a backup: %v", entries)
	}
}

func TestGatewayUpgradeWithoutBackupDoesNotMigrate(t *testing.T) {
	root := t.TempDir()
	dataDir := newGatewayDataDir(t, root)
	old := rewindGatewayStore(t, dataDir)
	// A regular file where the backup folder belongs makes writing it fail.
	if err := os.WriteFile(filepath.Join(root, "gateway-backups"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	g, err := New(Options{DataDir: dataDir, Version: "v0.5.62", Logger: log.New(&logs, "", 0)})
	if err == nil {
		g.Close()
		t.Fatal("gateway migrated without a backup")
	}
	line := logs.String()
	if !strings.Contains(line, "event=gateway.backup.before_upgrade_failed") || !strings.Contains(line, "err=") {
		t.Fatalf("log line does not name the failure: %q", line)
	}
	if got := snapshotSchemaVersion(t, filepath.Join(dataDir, "gateway.db")); got != old {
		t.Fatalf("schema = %d, want the old %d: migrations ran without a backup", got, old)
	}
}
