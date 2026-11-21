package gateway

import (
	"database/sql"
	"testing"
)

// stepNoticesSQL lists the notices the open tasks point at, one row for each
// task and session, under the column names the delivery_step_messages table
// had before the step tables rework.
const stepNoticesSQL = `(SELECT t.change_id, t.revision, t.step, t.for_step, t.after_merge, d.key AS session_id, d.value AS message_id FROM delivery_step_tasks t, json_each(t.notices) d)`

// mergerRowsSQL lists the registered mergers under the column names the
// project_mergers table had before the step tables rework.
const mergerRowsSQL = `(SELECT id AS project, merger_node AS node_id, merger_session AS session_id FROM repos WHERE merger_session<>'')`

// registerMerger makes session the merger of project on node, as SetMerger
// stores it.
func registerMerger(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, project, node, session string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO repos(id,display_name,created_at,updated_at,merger_node,merger_session) VALUES(?,?,0,0,?,?)
		ON CONFLICT(id) DO UPDATE SET merger_node=excluded.merger_node,merger_session=excluded.merger_session`, project, project, node, session); err != nil {
		t.Fatal(err)
	}
}

// linkStepNotice records that the open task of step sent message to session.
func linkStepNotice(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, change string, revision int, step, forStep string, afterMerge int, session, message string) {
	t.Helper()
	res, err := db.Exec(`UPDATE delivery_step_tasks SET notices=json_set(notices,'$."'||?||'"',?)
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=?`, session, message, change, revision, step, forStep, afterMerge)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("no open task for %s %s to link the notice to", change, step)
	}
}
