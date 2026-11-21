package gateway

import "testing"

// stepDroppedTables are the tables the step tables rework dropped.
var stepDroppedTables = []string{"delivery_step_messages", "delivery_step_delegates", "caller_step_escalations", "project_bar_next", "merge_train_fails", "project_mergers"}

// restoreStepTasksCompleted gives delivery_step_tasks back the done flag the
// step tables rework dropped, so the migrations before it and the rework itself
// find the column they read.
func restoreStepTasksCompleted(t *testing.T, db repoDB) {
	t.Helper()
	var has int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delivery_step_tasks') WHERE name='completed'`).Scan(&has); err != nil {
		t.Fatal(err)
	}
	if has == 0 {
		if _, err := db.Exec(`ALTER TABLE delivery_step_tasks ADD COLUMN completed INTEGER NOT NULL DEFAULT 0`); err != nil {
			t.Fatal(err)
		}
	}
}

// restoreMergerRows copies the registered mergers of the repos back into the
// project_mergers table, the way the migrations before the rework find them.
func restoreMergerRows(t *testing.T, db repoDB) {
	t.Helper()
	var has int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('repos') WHERE name='merger_session'`).Scan(&has); err != nil {
		t.Fatal(err)
	}
	if has == 0 {
		return
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO project_mergers(project,node_id,session_id) SELECT id,merger_node,merger_session FROM repos WHERE merger_session<>''`); err != nil {
		t.Fatal(err)
	}
}
