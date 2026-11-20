package gateway

import (
	"database/sql"
	"fmt"
)

// sideFoldMarker names the migration that folds small side tables into the
// table that holds their key (decision 0179, part 6). The work is in Go
// (foldSideTablesTx), so the SQL only carries the marker. Each step adds its
// columns only when absent and reads a side table only when it exists, so a
// replay after a rewind does not fail and loses no row.
const sideFoldMarker = "Side tables folded into their parents (decision 0179, part 6)"

const sideFoldMigration = `
-- ` + sideFoldMarker + `.
SELECT 1;
`

// foldSideTablesTx moves the rows of eight tables into their parents, drops
// the tables, and drops friction_status, which is the latest row of
// friction_status_history. Every side table has the key of its parent as its
// primary key, so a parent holds at most one row of each. A key that has no
// parent row yet (a failed wake of a session with no wake count, a model that
// only succeeded) gets one, with the defaults of the other columns.
func foldSideTablesTx(tx *sql.Tx) error {
	steps := []func(*sql.Tx) error{
		foldLearnedTx, foldSessionWakesTx, foldDefectAutoTx, foldModelSuccessesTx, foldMemberTicksTx, foldFrictionStatusTx,
	}
	for _, step := range steps {
		if err := step(tx); err != nil {
			return err
		}
	}
	return nil
}

func sideFoldTableExists(tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

// sideFoldAddColumns adds each column that the table does not have yet.
func sideFoldAddColumns(tx *sql.Tx, table string, columns [][2]string) error {
	for _, c := range columns {
		var has int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, c[0]).Scan(&has); err != nil {
			return err
		}
		if has == 0 {
			if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + c[0] + ` ` + c[1]); err != nil {
				return fmt.Errorf("add %s.%s: %w", table, c[0], err)
			}
		}
	}
	return nil
}

// sideFoldRun runs the statements of one side table when the table exists,
// then drops it.
func sideFoldRun(tx *sql.Tx, side string, statements ...string) error {
	exists, err := sideFoldTableExists(tx, side)
	if err != nil || !exists {
		return err
	}
	for _, q := range statements {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("fold %s: %w", side, err)
		}
	}
	_, err = tx.Exec(`DROP TABLE ` + side)
	return err
}

// foldLearnedTx: the origin and the history id of a suggestion, and the raw
// phrase it had before the cleaner changed it. A suggestion with no raw phrase
// row has NULL there, as a missing row read before.
func foldLearnedTx(tx *sql.Tx) error {
	if err := sideFoldAddColumns(tx, "learned_suggestions", [][2]string{
		{"origin", "TEXT NOT NULL DEFAULT ''"}, {"history_id", "INTEGER NOT NULL DEFAULT 0"}, {"raw_phrase", "TEXT"},
	}); err != nil {
		return err
	}
	if err := sideFoldRun(tx, "learned_suggestion_details",
		`UPDATE learned_suggestions SET origin=d.origin, history_id=d.history_id
		 FROM learned_suggestion_details d WHERE d.suggestion_id=learned_suggestions.id`); err != nil {
		return err
	}
	return sideFoldRun(tx, "learned_phrase_state",
		`UPDATE learned_suggestions SET raw_phrase=p.raw_phrase
		 FROM learned_phrase_state p WHERE p.suggestion_id=learned_suggestions.id`)
}

// foldSessionWakesTx: the last refused wake and the last wake attempt of a
// session. wake_failed_at is 0 when no refusal stands. wake_attempted_at is 0
// when the session was never woken from a message, which is older than any
// back-off cutoff.
func foldSessionWakesTx(tx *sql.Tx) error {
	if err := sideFoldAddColumns(tx, "session_wakes", [][2]string{
		{"wake_failed_at", "INTEGER NOT NULL DEFAULT 0"}, {"wake_failed_reason", "TEXT NOT NULL DEFAULT ''"},
		{"wake_attempted_at", "INTEGER NOT NULL DEFAULT 0"},
	}); err != nil {
		return err
	}
	if err := sideFoldRun(tx, "session_wake_failures",
		`INSERT INTO session_wakes(session_id,wake_failed_at,wake_failed_reason)
		 SELECT session_id,failed_at,reason FROM session_wake_failures WHERE true
		 ON CONFLICT(session_id) DO UPDATE SET wake_failed_at=excluded.wake_failed_at, wake_failed_reason=excluded.wake_failed_reason`); err != nil {
		return err
	}
	return sideFoldRun(tx, "retired_session_wakes",
		`INSERT INTO session_wakes(session_id,wake_attempted_at)
		 SELECT session_id,attempted_at FROM retired_session_wakes WHERE true
		 ON CONFLICT(session_id) DO UPDATE SET wake_attempted_at=excluded.wake_attempted_at`)
}

// foldDefectAutoTx: the change, kind and revision of a defect that agent-net
// recorded itself. The columns are NULL for a defect filed by hand, so the
// unique index lets any number of those stand and keeps one automatic defect
// for each change, kind and revision. The time of the side row was the time
// of the defect, so it is not kept.
func foldDefectAutoTx(tx *sql.Tx) error {
	if err := sideFoldAddColumns(tx, "worker_defects", [][2]string{
		{"auto_change_id", "TEXT"}, {"auto_kind", "TEXT"}, {"auto_revision", "INTEGER"},
	}); err != nil {
		return err
	}
	if err := sideFoldRun(tx, "worker_defect_auto",
		`UPDATE worker_defects SET auto_change_id=a.change_id, auto_kind=a.kind, auto_revision=a.revision
		 FROM worker_defect_auto a WHERE a.defect_id=worker_defects.id`); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS worker_defects_auto_key ON worker_defects(auto_change_id, auto_kind, auto_revision)`)
	return err
}

// foldModelSuccessesTx: the last success of a model on a machine, next to its
// last failure. A model that only succeeded gets a row with at=0 and
// auth_at=0, like a model with only an auth failure already has.
func foldModelSuccessesTx(tx *sql.Tx) error {
	if err := sideFoldAddColumns(tx, "model_failures", [][2]string{{"success_at", "INTEGER NOT NULL DEFAULT 0"}}); err != nil {
		return err
	}
	return sideFoldRun(tx, "model_successes",
		`INSERT INTO model_failures(node_id,harness,model,at,success_at)
		 SELECT node_id,harness,model,0,at FROM model_successes WHERE true
		 ON CONFLICT(node_id,harness,model) DO UPDATE SET success_at=excluded.success_at`)
}

// foldMemberTicksTx: the fingerprint a lead's loop tick last listed, next to
// the one the blocked-member notice last told. A session with only a tick row
// gets a row with an empty text_hash, which reads as no notice.
func foldMemberTicksTx(tx *sql.Tx) error {
	if err := sideFoldAddColumns(tx, "member_blocked_notices", [][2]string{{"tick_hash", "TEXT NOT NULL DEFAULT ''"}}); err != nil {
		return err
	}
	return sideFoldRun(tx, "member_blocked_ticks",
		`INSERT INTO member_blocked_notices(session_id,text_hash,at,tick_hash)
		 SELECT session_id,'',0,text_hash FROM member_blocked_ticks WHERE true
		 ON CONFLICT(session_id) DO UPDATE SET tick_hash=excluded.tick_hash`)
}

// foldFrictionStatusTx drops friction_status. The current mark of a report is
// its latest history row (friction.go reads it so). A row whose latest history
// row differs, or has none, gets its current values appended to the history
// first, so no report changes status.
func foldFrictionStatusTx(tx *sql.Tx) error {
	return sideFoldRun(tx, "friction_status",
		`INSERT INTO friction_status_history(friction_id,status,changed_by,changed_at,link,note)
		 SELECT s.friction_id,s.status,s.changed_by,s.changed_at,s.link,s.note FROM friction_status s
		 WHERE NOT EXISTS (SELECT 1 FROM friction_status_history h
		  WHERE h.id=(SELECT MAX(id) FROM friction_status_history WHERE friction_id=s.friction_id)
		  AND h.status=s.status AND h.changed_by=s.changed_by AND h.changed_at=s.changed_at AND h.link=s.link AND h.note=s.note)`)
}
