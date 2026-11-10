package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// BusyHoldLimit is how long a message waits for a session that stays busy.
// After this the gateway delivers the held messages anyway, so a session that
// is stuck reporting busy is not starved (decision 0130, part c).
const BusyHoldLimit = 30 * time.Minute

// busyHoldEligible tells whether the gateway should keep a message for this
// recipient instead of pushing it into the session now. Workers are left out:
// their daemon already queues messages for the turn.
func (s *Store) busyHoldEligible(recipient string) (bool, error) {
	a, ok, err := s.Agent(recipient)
	if err != nil || !ok {
		return false, err
	}
	if !a.Online || a.Worker != nil || a.Status != "busy" {
		return false, nil
	}
	switch a.Harness {
	case "bot", protocol.OwnerHarness, "cto":
		return false, nil
	}
	return true, nil
}

// HoldForBusy keeps a stored message at the gateway because its recipient is
// in a turn. It reports whether the message is now held.
func (s *Store) HoldForBusy(m protocol.Message) bool {
	now := s.now().UnixNano()
	res, err := s.db.Exec(`UPDATE messages SET held_for_busy=1, held_at=? WHERE id=? AND state='stored' AND held_for_busy=0`, now, m.ID)
	if err != nil {
		s.logf("error", "where", "hold for busy", "msg", m.ID, "err", err)
		return false
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false
	}
	busy := s.busyForSeconds(m.To, now)
	s.Event("msg.held_busy", "", m.To, m.ID, "")
	s.logStep("msg.held_busy", m.ID, "busy_for_s", busy, "reason", "recipient busy")
	return true
}

// busyForSeconds is how long the session has been in its current busy stretch.
func (s *Store) busyForSeconds(sessionID string, now int64) int64 {
	var since int64
	if err := s.db.QueryRow(`SELECT COALESCE(busy_since,0) FROM sessions WHERE id=?`, sessionID).Scan(&since); err != nil || since <= 0 {
		return 0
	}
	if now < since {
		return 0
	}
	return (now - since) / int64(time.Second)
}

// ReleaseBusyHold builds the one message that carries everything held for a
// session and returns it, ready to dispatch. force ignores the current status:
// the 30-minute fallback uses it. It returns no message when the session is
// still busy, is offline, or has nothing held.
func (s *Store) ReleaseBusyHold(sessionID string, force bool) (protocol.Message, int, error) {
	if !force {
		a, ok, err := s.Agent(sessionID)
		if err != nil {
			return protocol.Message{}, 0, err
		}
		if !ok || !a.Online || a.Status == "busy" {
			return protocol.Message{}, 0, nil
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, 0, err
	}
	defer tx.Rollback()
	now := s.now()
	rows, err := tx.Query(msgSelect+` WHERE m.recipient=? AND m.held_for_busy=1 AND m.state='stored' AND m.batch_id='' AND m.expires_at>?
		ORDER BY m.urgent DESC, m.created_at DESC, m.rowid DESC`, sessionID, now.UnixNano())
	if err != nil {
		return protocol.Message{}, 0, err
	}
	var held []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return protocol.Message{}, 0, err
		}
		held = append(held, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.Message{}, 0, err
	}
	rows.Close()
	if len(held) == 0 {
		return protocol.Message{}, 0, nil
	}
	busy := s.busyForSeconds(sessionID, now.UnixNano())
	if len(held) == 1 {
		if _, err := tx.Exec(`UPDATE messages SET held_for_busy=0, held_at=0 WHERE id=?`, held[0].ID); err != nil {
			return protocol.Message{}, 0, err
		}
		if err := tx.Commit(); err != nil {
			return protocol.Message{}, 0, err
		}
		carrier := held[0]
		carrier.HeldForBusy = false
		s.Event("msg.released_busy", "", sessionID, carrier.ID, "")
		s.logStep("msg.released_busy", carrier.ID, "count", 1, "busy_for_s", busy)
		return carrier, 1, nil
	}
	carrier, err := s.insertBusyBatch(tx, sessionID, held, now)
	if err != nil {
		return protocol.Message{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Message{}, 0, err
	}
	s.Event("msg.batch", "", sessionID, carrier.ID, fmt.Sprintf("messages=%d", len(held)))
	s.logStep("msg.batch", carrier.ID, "count", len(held), "busy_for_s", busy)
	return carrier, len(held), nil
}

// insertBusyBatch writes the batch message and marks every held message as
// part of it. The batch keeps each original message in its Batch field.
func (s *Store) insertBusyBatch(tx *sql.Tx, sessionID string, held []protocol.Message, now time.Time) (protocol.Message, error) {
	newest := held[0]
	kind := newest.Type
	for _, m := range held {
		if m.Type == protocol.TypeRequest {
			kind = protocol.TypeRequest
			break
		}
	}
	urgent := false
	expires := newest.ExpiresAt
	for _, m := range held {
		urgent = urgent || m.Urgent
		if m.ExpiresAt.Before(expires) {
			expires = m.ExpiresAt
		}
	}
	batchJSON, err := json.Marshal(held)
	if err != nil {
		return protocol.Message{}, err
	}
	carrier := protocol.Message{
		ID: protocol.NewID("m"), ConversationID: newest.ConversationID,
		From: newest.From, To: sessionID, Type: kind, Body: busyBatchBody(held),
		Hop: newest.Hop, IdempotencyKey: "batch-" + protocol.NewID("k"),
		CreatedAt: now.UTC(), ExpiresAt: expires, State: protocol.StateStored,
		FromCTO: newest.FromCTO, OwnerApproved: newest.OwnerApproved,
		Urgent: urgent, Batch: held,
	}
	if _, err := tx.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop,
		idem_key, created_at, expires_at, state, sender_cto, owner_approved, urgent, batch_json) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		carrier.ID, carrier.ConversationID, "", carrier.From, carrier.To, carrier.Type, carrier.Body, carrier.Hop,
		carrier.IdempotencyKey, now.UnixNano(), expires.UnixNano(), carrier.State, carrier.FromCTO, carrier.OwnerApproved,
		carrier.Urgent, string(batchJSON)); err != nil {
		return protocol.Message{}, err
	}
	args := []any{carrier.ID}
	for _, m := range held {
		args = append(args, m.ID)
	}
	if _, err := tx.Exec(`UPDATE messages SET held_for_busy=0, held_at=0, state='dispatched', batch_id=?
		WHERE id IN (?`+strings.Repeat(",?", len(held)-1)+`)`, args...); err != nil {
		return protocol.Message{}, err
	}
	return carrier, nil
}

// busyBatchBody is the one text a session sees for a batch. The header is the
// owner's wording (decision 0130, part d): act on the newest and read the
// older ones as context. Each original message keeps its ID.
func busyBatchBody(held []protocol.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d messages arrived while you were busy. Act on the newest; read the older ones as context.", len(held))
	for i, m := range held {
		name := m.FromName
		if name == "" {
			name = m.From
		}
		fmt.Fprintf(&b, "\n\n--- %d of %d: message %s from %s", i+1, len(held), m.ID, name)
		if m.Urgent {
			b.WriteString(", urgent")
		}
		b.WriteString(" ---\n")
		b.WriteString(m.Body)
	}
	return b.String()
}

// StuckBusySessions lists the sessions with a message that has waited longer
// than BusyHoldLimit while the session keeps reporting busy.
func (s *Store) StuckBusySessions() ([]string, error) {
	now := s.now().UnixNano()
	cutoff := now - int64(BusyHoldLimit)
	rows, err := s.db.Query(`SELECT DISTINCT recipient FROM messages
		WHERE held_for_busy=1 AND state='stored' AND batch_id='' AND held_at>0 AND held_at<=? AND expires_at>?`,
		cutoff, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// queueStatusFor returns a message with its queue fields filled in, if it is
// still waiting. It is the value msg.send and agent_message_status return.
func (s *Store) queueStatusFor(m protocol.Message) protocol.Message {
	return s.enrichQueue(m)
}

// setBusySince records the start of a busy stretch, and keeps the start of the
// last one when the session stops being busy, so the release can say how long
// it ran. The caller passes the status the session had before this report.
func setBusySince(tx execer, sessionID, status, previous string, now int64) error {
	switch {
	case status == "busy" && previous != "busy":
		_, err := tx.Exec(`UPDATE sessions SET busy_since=? WHERE id=?`, now, sessionID)
		return err
	case status != "busy":
		_, err := tx.Exec(`UPDATE sessions SET busy_since=0 WHERE id=? AND busy_since<>0`, sessionID)
		return err
	}
	return nil
}

// clearBusySince forgets the start of the last busy stretch after a release.
func (s *Store) clearBusySince(sessionID string) {
	_, _ = s.db.Exec(`UPDATE sessions SET busy_since=0 WHERE id=?`, sessionID)
}
