package gateway

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/niparis/agent-net/internal/protocol"
)

// frontDoorFlushMigration closes the open Desk items that waited on the owner
// before the front door (plan step (d), decision 0150). The owner's words,
// typed to the agent-net lead on 2026-11-12: "note that i dont care if the
// inbox is flushed. Right now it's pretty bad, i only use it when forced."
// The SQL is only a marker: the work is flushOwnerAsksOnceTx.
const frontDoorFlushMigration = `SELECT 1 /* Front door flush of open owner items (decision 0150) */;`

// frontDoorFlushKey marks the flush in owner_settings, with the number of
// items it closed. A store that has it never flushes again, so a replay of
// the migrations closes nothing made after the front door.
const frontDoorFlushKey = "front_door_flush_v1"

// flushKeptSQL keeps the owner items that something else still waits on. A
// delivery approval gates a merge and a permission ask its proposal; neither
// is asked again, so closing them would strand the change or the proposal.
var flushKeptSQL = `id NOT IN (SELECT item_id FROM delivery_step_inbox) AND source_ref NOT LIKE '` + permissionAskPrefix + `%'`

// flushOwnerAsksOnceTx closes every open item that waits on the owner, once.
// It keeps each row, with the reason in answer and the time in answered_at.
// A duplicate that joined a closed item closes with it (trigger
// inbox_items_joined_settle). Nothing is sent to the askers.
func (s *Store) flushOwnerAsksOnceTx(tx *sql.Tx) error {
	var done string
	err := tx.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, frontDoorFlushKey).Scan(&done)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	now := s.now().UnixNano()
	res, err := tx.Exec(`UPDATE inbox_items SET status='closed', answer=?, answered_at=?
		WHERE status='open' AND next_who IN ('','owner') AND `+flushKeptSQL, protocol.AskReasonFlushedByFrontDoor, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		text := fmt.Sprintf("Front door: closed %d open Desk items that waited on you (%s)", n, protocol.AskReasonFlushedByFrontDoor)
		if err := s.deskLog(tx, "desk", "", "", text, "", nil); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES(?,?,?)`, frontDoorFlushKey, strconv.FormatInt(n, 10), now)
	return err
}
