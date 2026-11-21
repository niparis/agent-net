package gateway

import (
	"path/filepath"
	"sync"
	"testing"
)

// learnedDroppedTables are the tables the learned tables rework dropped. The
// migrations before it still name them, so a replay that starts before the
// rework needs them back.
var learnedDroppedTables = []string{"charter_signals", "signal_prompts", "learned_message_flow", "learned_phrase_runs", "learned_views", "friction_status_history"}

var learnedDroppedSQL struct {
	once sync.Once
	sql  map[string]string
}

// recreateLearnedDroppedTables makes back each dropped table that is missing,
// with the statement it had before the rework. The rows stay gone: the rework
// copied them into columns, and an empty table copies nothing on a replay.
func recreateLearnedDroppedTables(t *testing.T, db repoDB) {
	t.Helper()
	learnedDroppedSQL.once.Do(func() {
		learnedDroppedSQL.sql = readLearnedDroppedSQL(t)
	})
	for _, name := range learnedDroppedTables {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			continue
		}
		if _, err := db.Exec(learnedDroppedSQL.sql[name]); err != nil {
			t.Fatalf("recreate %s: %v", name, err)
		}
	}
}

// readLearnedDroppedSQL migrates a scratch file up to the rework and copies the
// statements of the dropped tables from its schema.
func readLearnedDroppedSQL(t *testing.T) map[string]string {
	t.Helper()
	before := migrationVersion(t, learnedTablesMarker) - 1
	s := migrateOnly(t, filepath.Join(t.TempDir(), "before-learned.db"), before)
	defer s.Close()
	out := make(map[string]string, len(learnedDroppedTables))
	for _, name := range learnedDroppedTables {
		var stmt string
		if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&stmt); err != nil {
			t.Fatalf("schema of %s before the rework: %v", name, err)
		}
		out[name] = stmt
	}
	return out
}
