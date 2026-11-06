package gateway

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// prepareOldSessionIdentity removes the identity schema when a test rewinds
// schema_version. Migration 115 then runs as shipped. Earlier migrations may
// retain newer columns and use their own replay checks.
func prepareOldSessionIdentity(t *testing.T, s *Store) {
	t.Helper()
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&current); err != nil {
		t.Fatal(err)
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
	if current >= migrationVersion(t, "Explicit session and run identity") {
		return
	}
	if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS session_lifecycle_insert; DROP TRIGGER IF EXISTS session_lifecycle_update; DROP TRIGGER IF EXISTS session_identity_repo_update; DROP TABLE IF EXISTS session_agent_history`); err != nil {
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

func TestMigrationInboxAndSessionIdentityReplay(t *testing.T) {
	for _, migration := range []struct {
		marker  string
		version int
	}{
		{"ALTER TABLE inbox_items ADD COLUMN options", 114},
		{"Explicit session and run identity", 115},
		{"Owner changes to a registered session's agent", 116},
	} {
		if got := migrationVersion(t, migration.marker); got != migration.version {
			t.Fatalf("migration %q is %d, want %d", migration.marker, got, migration.version)
		}
	}
	r := newDeliveryRig(t)
	item, err := r.s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: r.caller, Node: "demo", Text: "Choose", Options: []string{"one", "two"}, AskerName: "Reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	// Older migration tests rewind the version but retain the Inbox columns.
	// Replaying 114 must preserve their data before identity migration 115.
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=114`); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	items, err := r.s.ListInbox()
	if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].AskerName != item.AskerName || !reflect.DeepEqual(items[0].Options, item.Options) {
		t.Fatalf("inbox after replay: %+v, %v", items, err)
	}
	var agent, repo string
	if err := r.s.db.QueryRow(`SELECT COALESCE(agent_id,''),COALESCE(repo_id,'') FROM sessions WHERE id=?`, r.caller).Scan(&agent, &repo); err != nil || agent != "demo" || repo != "demo" {
		t.Fatalf("session identity after replay: %q, %q, %v", agent, repo, err)
	}
}
