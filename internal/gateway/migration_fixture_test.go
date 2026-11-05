package gateway

import (
	"fmt"
	"testing"
)

// prepareOldSessionIdentity restores the pre-114 shape for a test that
// deliberately rewinds schema_version. Production migrations then run exactly
// as shipped, on a schema that could actually have that version.
func prepareOldSessionIdentity(t *testing.T, s *Store) {
	t.Helper()
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current >= migrationVersion(t, "Explicit session and run identity") {
		return
	}
	if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS session_identity_repo_update; DROP TABLE IF EXISTS session_agent_history`); err != nil {
		t.Fatal(err)
	}
	for _, col := range []struct{ table, name string }{{"sessions", "agent_id"}, {"sessions", "repo_id"}, {"worker_runs", "caller_agent_id"}, {"worker_starts", "caller_agent_id"}} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", col.table, col.name)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func migrateOldFixture(t *testing.T, s *Store) error {
	t.Helper()
	prepareOldSessionIdentity(t, s)
	return s.migrate()
}
