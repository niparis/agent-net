package gateway

import (
	"database/sql"
	"fmt"
	"testing"
)

// setSchemaVersion rewrites the stored migration version without going through
// OpenStore, as an older or newer binary would find it on disk.
func setSchemaVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_version`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenStoreAcceptsCurrentSchema(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("opening a database at the current version: %v", err)
	}
	defer s.Close()
	if v, err := s.SchemaVersion(); err != nil || v != len(migrations) {
		t.Fatalf("schema version %d, %v; want %d", v, err, len(migrations))
	}
}

func TestOpenStoreRefusesNewerSchema(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	newer := len(migrations) + 1
	setSchemaVersion(t, path, newer)

	s, err := OpenStore(path)
	if err == nil {
		s.Close()
		t.Fatalf("opened a database at schema version %d; want a refusal", newer)
	}
	want := fmt.Sprintf("gateway.db schema version %d is newer than this agent-net (knows %d); upgrade agent-net or restore the backup taken before the upgrade", newer, len(migrations))
	if err.Error() != want {
		t.Fatalf("error %q, want %q", err.Error(), want)
	}
}

func TestOpenStoreMigratesOlderSchemaUp(t *testing.T) {
	path := seededStorePath(t, "gw.db")
	older := len(migrations) - 1
	setSchemaVersion(t, path, older)

	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("opening a database at schema version %d: %v", older, err)
	}
	defer s.Close()
	if v, err := s.SchemaVersion(); err != nil || v != len(migrations) {
		t.Fatalf("schema version %d, %v; want %d", v, err, len(migrations))
	}
}
