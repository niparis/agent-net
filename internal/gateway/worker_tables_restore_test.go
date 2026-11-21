package gateway

import "testing"

// workerDroppedTables are the tables the worker tables rework dropped.
var workerDroppedTables = []string{"worker_usage", "worker_visual_proof", "worker_rate_offers", "worker_brief_defects",
	"worker_brief_rejections", "quota_budgets", "start_override_facts", "table_versions", "project_policies"}

// workerDroppedColumns are the copied columns the rework dropped. An older
// migration creates an index on some of them, so a replay needs them back.
var workerDroppedColumns = []struct{ table, name string }{
	{"worker_ratings", "harness"}, {"worker_ratings", "model"}, {"worker_ratings", "effort"}, {"worker_ratings", "difficulty"},
	{"worker_defects", "harness"}, {"worker_defects", "model"}, {"worker_defects", "effort"}, {"worker_defects", "difficulty"},
}

// recreateWorkerDroppedColumns adds back each dropped column that is missing.
func recreateWorkerDroppedColumns(t *testing.T, db repoDB) {
	t.Helper()
	for _, col := range workerDroppedColumns {
		var has int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.name).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.name + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// dropWorkerVersionTriggers removes the triggers that count worker_runs changes
// in gateway_state. Versions before the rework have no such triggers, and a
// replay that drops gateway_state or a column checks every trigger.
func dropWorkerVersionTriggers(t *testing.T, db repoDB) {
	t.Helper()
	for _, name := range []string{"worker_runs_version_insert", "worker_runs_version_update", "worker_runs_version_delete"} {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
}

// legacyPolicyDB makes sure project_policies exists on s and returns its
// handle, for the tests that seed the rows an older version kept there.
func legacyPolicyDB(t *testing.T, s *Store) *storeDB {
	t.Helper()
	restoreTablesAt(t, s.db, migrationVersion(t, workerTablesMarker)-1, "project_policies")
	return s.db
}
