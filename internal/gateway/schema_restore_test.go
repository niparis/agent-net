package gateway

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// schemaTablesCache holds the tables of each schema version that a replay has
// started from. Building one migrates a scratch file, so each version is built
// once per test binary.
var schemaTablesCache = struct {
	sync.Mutex
	byVersion map[int]map[string]string
}{byVersion: map[int]map[string]string{}}

// tablesAtVersion returns the CREATE TABLE statement of each table that a
// database migrated to version has.
func tablesAtVersion(t *testing.T, version int) map[string]string {
	t.Helper()
	schemaTablesCache.Lock()
	defer schemaTablesCache.Unlock()
	if tables, ok := schemaTablesCache.byVersion[version]; ok {
		return tables
	}
	s := migrateOnly(t, filepath.Join(t.TempDir(), "schema.db"), version)
	defer s.Close()
	rows, err := s.db.Query(`SELECT name,sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string]string{}
	for rows.Next() {
		var name, stmt string
		if err := rows.Scan(&name, &stmt); err != nil {
			t.Fatal(err)
		}
		tables[name] = stmt
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	schemaTablesCache.byVersion[version] = tables
	return tables
}

// restoreDroppedSince puts back each table that existed at schema version cur
// and is missing now. A migration after cur dropped it, and a replay from cur
// drops it again, so it must exist first. The rows stay gone: the drop
// migration already moved them.
func restoreDroppedSince(t *testing.T, db repoDB, cur int) {
	t.Helper()
	for name, stmt := range tablesAtVersion(t, cur) {
		createIfMissing(t, db, name, stmt)
	}
}

// restoreTablesAt puts back the named tables that are missing and that existed
// at schema version v, each with the statement it had there. A test that writes rows into an old
// table before it rewinds schema_version needs the table first; the rewind
// itself restores the rest through restoreDroppedSince.
func restoreTablesAt(t *testing.T, db repoDB, v int, names ...string) {
	t.Helper()
	tables := tablesAtVersion(t, v)
	for _, name := range names {
		// A table that did not exist at v is created by an older migration
		// that the replay still runs.
		if stmt, ok := tables[name]; ok {
			createIfMissing(t, db, name, stmt)
		}
	}
}

func createIfMissing(t *testing.T, db repoDB, name, stmt string) {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("restore %s: %v", name, err)
		}
	}
}

// The learned tables rework dropped charter_signals. A test that rewinds before
// the rework needs the table back, empty, because the rework already moved its
// rows into messages.
func TestRestoreBringsBackATableALaterMigrationDropped(t *testing.T) {
	s, _ := newTestStore(t)
	before := migrationVersion(t, learnedTablesMarker) - 1
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, before); err != nil {
		t.Fatal(err)
	}
	if tableExists(t, s, "charter_signals") {
		t.Fatal("charter_signals is still there after the rework")
	}
	restoreRepoSideTables(t, s.db)
	if !tableExists(t, s, "charter_signals") {
		t.Fatal("charter_signals is missing after the restore")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM charter_signals`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("restored charter_signals holds %d rows, want none", n)
	}
}

// The OAuth clients rework dropped oauth_clients and app_refresh_budget. A test
// that rewinds before it needs both back, empty, because the rework already
// moved their facts into apps and app_tokens.
func TestRestoreBringsBackTheOAuthTables(t *testing.T) {
	s, _ := newTestStore(t)
	before := migrationVersion(t, oauthClientsMarker) - 1
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, before); err != nil {
		t.Fatal(err)
	}
	restoreRepoSideTables(t, s.db)
	for _, name := range []string{"oauth_clients", "app_refresh_budget"} {
		if !tableExists(t, s, name) {
			t.Fatalf("%s is missing after the restore", name)
		}
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + name).Scan(&n); err != nil || n != 0 {
			t.Fatalf("restored %s holds %d rows, want none (%v)", name, n, err)
		}
	}
}

// Only replayMigrations calls migrate in a test. A direct call replays without
// the tables that later migrations dropped, and fails on the first drop it
// reaches. The helper file itself is the one exception.
func TestTestsReplayThroughReplayMigrations(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	direct := regexp.MustCompile(`\.migrate\(\)`)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") || name == "repo_fold_migration_test.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if direct.Match(src) {
			t.Errorf("%s calls migrate directly; use replayMigrations", name)
		}
	}
}
