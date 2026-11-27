package gateway

import (
	"fmt"
	"testing"
)

// prepareOldSessionIdentity removes later session columns when a test rewinds
// schema_version. Identity and stall migrations then run against old schemas.
// Earlier migrations may retain newer columns and use their own replay checks.
func prepareOldSessionIdentity(t *testing.T, s *Store) {
	t.Helper()
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	restoreRepoSideTables(t, s.db)
	if current < migrationVersion(t, "Explicit session and run identity") {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS session_lifecycle_insert; DROP TRIGGER IF EXISTS session_lifecycle_update; DROP TRIGGER IF EXISTS session_identity_repo_update; DROP INDEX IF EXISTS nudge_sessions_limited; DROP INDEX IF EXISTS nudge_repo_sessions_limited`); err != nil {
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
	if current < migrationVersion(t, "Session harness activity and unexpected loss") {
		for _, name := range []string{"harness_activity", "harness_lost"} {
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name=?`, name).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				if _, err := s.db.Exec(`ALTER TABLE sessions DROP COLUMN ` + name); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

}

func migrateOldFixture(t *testing.T, s *Store) error {
	t.Helper()
	prepareOldSessionIdentity(t, s)
	return replayMigrations(t, s)
}

func TestMigrationInboxAndSessionIdentityReplay(t *testing.T) {
	for _, migration := range []struct {
		marker  string
		version int
	}{
		{"ALTER TABLE inbox_items ADD COLUMN options", 114},
		{"Explicit session and run identity", 115},
		{"Owner changes to a registered session's agent", 116},
		{"worker early quit evidence", 127},
		{"Session harness activity and unexpected loss", 128},
	} {
		if got := migrationVersion(t, migration.marker); got != migration.version {
			t.Fatalf("migration %q is %d, want %d", migration.marker, got, migration.version)
		}
	}
	r := newDeliveryRig(t)
	// Replaying migrations from 115 must preserve session identity.
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=115`); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	var agent, repo string
	if err := r.s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,'') FROM sessions WHERE id=?`, r.caller).Scan(&agent, &repo); err != nil || agent != "demo" || repo != "demo" {
		t.Fatalf("session identity after replay: %q, %q, %v", agent, repo, err)
	}
}
