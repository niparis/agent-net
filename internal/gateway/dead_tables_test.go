package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Production migration code only moves a store forward. Rebuilding dropped
// tables or older column layouts so a test can rewind schema_version and replay
// shipped steps belongs in _test.go helpers.
func TestProductionCodeHasNoRewindHelpers(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	rewindOnly := []string{
		"ensureOldCharterTablesTx",
		"oneCharterStoreStep",
		"restoreLegacyStateTables",
		"legacyStateTables",
		"gatewayStateMigrationIndex",
		"graphNodesReplay",
		"graphNodesFoldIndex",
		"graphNodesCreateIndex",
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, symbol := range rewindOnly {
			if strings.Contains(text, symbol) {
				t.Errorf("production file %s still carries rewind helper %s", name, symbol)
			}
		}
	}
}

// deadTables are the tables that no code reads or writes any more. The schema
// reduction (part 1) drops them. Each one is named by an older migration, which
// still runs before the drop, so an old database replays cleanly.
var deadTables = []string{
	"chief_corrections",
	"delivery_settings_legacy",
	"skill_suggestions",
	"playbook_notes",
	"session_agent_history",
	"agent_charter_history",
	"node_scopes_history",
}

// tableExists reports whether the database has a table of that name.
func tableExists(t *testing.T, s *Store, name string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestDeadTablesAreDropped(t *testing.T) {
	s, _ := newTestStore(t)
	for _, name := range deadTables {
		if tableExists(t, s, name) {
			t.Errorf("table %s is still in the schema; the drop migration should remove it", name)
		}
	}
}

// deadTableSQL is the CREATE TABLE statement of each dead table, as the
// migrations before the drop make it. It is read once per test binary.
var deadTableSQL struct {
	once sync.Once
	sql  map[string]string
}

// recreateDeadTables puts the dead tables back into a store that a test has
// rewound to an older schema version. An older database still has them, and the
// migrations that read them replay before the drop, so they must exist.
func recreateDeadTables(t *testing.T, s *Store) {
	t.Helper()
	recreateDeadTablesDB(t, s.db)
}

func recreateDeadTablesDB(t *testing.T, db repoDB) {
	t.Helper()
	deadTableSQL.once.Do(func() {
		deadTableSQL.sql = readDeadTableSQL(t)
	})
	for _, name := range deadTables {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			continue
		}
		if _, err := db.Exec(deadTableSQL.sql[name]); err != nil {
			t.Fatalf("recreate %s: %v", name, err)
		}
	}
}

// readDeadTableSQL migrates a scratch file up to the drop and copies the
// statements of the dead tables from its schema.
func readDeadTableSQL(t *testing.T) map[string]string {
	t.Helper()
	dropped := migrationVersion(t, "Schema reduction part 1: drop the tables")
	s := migrateOnly(t, filepath.Join(t.TempDir(), "before-drop.db"), dropped-1)
	defer s.Close()
	out := make(map[string]string, len(deadTables))
	for _, name := range deadTables {
		var stmt string
		if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&stmt); err != nil {
			t.Fatalf("schema of %s before the drop: %v", name, err)
		}
		out[name] = stmt
	}
	return out
}
