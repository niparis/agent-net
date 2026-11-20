package gateway

import (
	"database/sql"
	"errors"
	"fmt"
)

// sideTablesFoldMarker names the migration that folds two side tables into
// the row they describe (schema reduction, part 5):
//
//   - delivery_revision_stats into delivery_revisions: diff_stat, signals and
//     handback_check. One row per revision, the same key.
//   - message_files into messages: files. One manifest per message.
//
// delivery_step_inbox stays. An approval for the owner is queued without a
// task row (the owner is a person, not a node), so an inbox row does not
// always have a task row to fold into.
//
// The work is in Go (foldSideTablesTx), so the SQL only carries the marker.
const sideTablesFoldMarker = "Side tables folded into delivery_revisions and messages"

const sideTablesFoldMigration = `
-- ` + sideTablesFoldMarker + `: diff stat, signals and hand-back check on the revision, the file manifest on the message.
SELECT 1;
`

// foldSideTablesTx adds the columns, copies every row and drops the side
// tables, in the caller's transaction. A replay is safe: a column that exists
// is left alone, a table that is gone is skipped, and a replayed earlier
// migration that made an empty side table again is folded and dropped again.
func foldSideTablesTx(tx *sql.Tx) error {
	for _, col := range []struct{ table, name, decl string }{
		{"delivery_revisions", "diff_stat", "TEXT NOT NULL DEFAULT ''"},
		{"delivery_revisions", "signals", "TEXT NOT NULL DEFAULT '[]'"},
		{"delivery_revisions", "handback_check", "TEXT NOT NULL DEFAULT ''"},
		{"messages", "files", "TEXT NOT NULL DEFAULT ''"},
	} {
		has, err := columnExistsTx(tx, col.table, col.name)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.name + ` ` + col.decl); err != nil {
			return err
		}
	}
	if err := foldRevisionStatsTx(tx); err != nil {
		return err
	}
	return foldMessageFilesTx(tx)
}

func tableExistsTx(tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func columnExistsTx(tx *sql.Tx, table, column string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&n)
	return n > 0, err
}

// foldRevisionStatsTx refuses to drop a stat row that has no revision row to
// move to: that row would be lost, and the owner should see it, not find out
// later. A revision row is written with its change, so none is expected.
func foldRevisionStatsTx(tx *sql.Tx) error {
	exists, err := tableExistsTx(tx, "delivery_revision_stats")
	if err != nil || !exists {
		return err
	}
	var change string
	var revision int
	err = tx.QueryRow(`SELECT change_id, revision FROM delivery_revision_stats st
		WHERE NOT EXISTS (SELECT 1 FROM delivery_revisions r WHERE r.change_id=st.change_id AND r.number=st.revision) LIMIT 1`).Scan(&change, &revision)
	if err == nil {
		return fmt.Errorf("delivery_revision_stats has a row for change %s revision %d, but delivery_revisions has no such revision: fix or remove the row before this gateway folds the table", change, revision)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// A table made again by a replayed old migration may lack the later columns.
	signals, handback := `'[]'`, `''`
	for _, col := range []struct {
		name string
		dst  *string
	}{{"signals", &signals}, {"handback_check", &handback}} {
		has, err := columnExistsTx(tx, "delivery_revision_stats", col.name)
		if err != nil {
			return err
		}
		if has {
			*col.dst = "st." + col.name
		}
	}
	if _, err := tx.Exec(`UPDATE delivery_revisions SET diff_stat=st.diff_stat, signals=` + signals + `, handback_check=` + handback + `
		FROM delivery_revision_stats st WHERE st.change_id=delivery_revisions.change_id AND st.revision=delivery_revisions.number`); err != nil {
		return err
	}
	_, err = tx.Exec(`DROP TABLE delivery_revision_stats`)
	return err
}

// foldMessageFilesTx moves each manifest to its message. A manifest whose
// message is gone is dropped with the table: retention already deletes such
// rows, and no reader can reach them.
func foldMessageFilesTx(tx *sql.Tx) error {
	exists, err := tableExistsTx(tx, "message_files")
	if err != nil || !exists {
		return err
	}
	if _, err := tx.Exec(`UPDATE messages SET files=mf.files FROM message_files mf WHERE mf.message_id=messages.id`); err != nil {
		return err
	}
	_, err = tx.Exec(`DROP TABLE message_files`)
	return err
}
