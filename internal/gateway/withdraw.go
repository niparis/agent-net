package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// Undelivered states: a message in one of them has not reached the recipient's
// harness, so its sender or the owner may take it back. A held request is
// still undelivered: it waits for the owner's answer.
const withdrawableStates = `('stored','dispatched','held','failed')`

// Withdraw takes back one undelivered message. A session may withdraw its own
// message; an owner session and the CTO may withdraw any undelivered message
// to a session (decision 0130, part 4). A message the harness has accepted
// cannot be withdrawn.
func (s *Store) Withdraw(id, actor string) (protocol.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	m, ok, err := s.message(tx, "m.id=?", id)
	if err != nil {
		return protocol.Message{}, err
	}
	if !ok {
		return protocol.Message{}, reject("unknown message %q", id)
	}
	owner, err := s.mayWithdrawAny(tx, actor)
	if err != nil {
		return protocol.Message{}, err
	}
	if !owner && m.From != actor {
		return protocol.Message{}, reject("you can only withdraw your own message; %s was sent by another session", id)
	}
	if err := s.withdrawOne(tx, m, s.now().UnixNano()); err != nil {
		return protocol.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Message{}, err
	}
	s.Event("msg.withdrawn", "", m.To, id, "")
	s.logStep("msg.withdrawn", id, "by", actor)
	out, _, err := s.Message(id)
	return out, err
}

// WithdrawOlderThan withdraws every undelivered message to the recipient of
// id that is as old as or older than it, including id. Only an owner session
// or the CTO may do this.
func (s *Store) WithdrawOlderThan(id, actor string) ([]protocol.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	target, ok, err := s.message(tx, "m.id=?", id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, reject("unknown message %q", id)
	}
	owner, err := s.mayWithdrawAny(tx, actor)
	if err != nil {
		return nil, err
	}
	if !owner {
		return nil, reject("only an owner session or the CTO can withdraw all messages older than %s", id)
	}
	rows, err := tx.Query(`SELECT id FROM messages WHERE recipient=? AND created_at<=? AND state IN `+withdrawableStates+`
		ORDER BY created_at, rowid`, target.To, target.CreatedAt.UnixNano())
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var mid string
		if err := rows.Scan(&mid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, mid)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	now := s.now().UnixNano()
	var out []protocol.Message
	for _, mid := range ids {
		m, ok, err := s.message(tx, "m.id=?", mid)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if err := s.withdrawOne(tx, m, now); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i := range out {
		s.Event("msg.withdrawn", "", out[i].To, out[i].ID, "")
		s.logStep("msg.withdrawn", out[i].ID, "by", actor)
		out[i].State = protocol.StateWithdrawn
	}
	return out, nil
}

// mayWithdrawAny says whether actor is an owner session or the registered CTO
// session. The answer comes from the stored session and owner settings, never
// from a message body or a sender flag.
func (s *Store) mayWithdrawAny(q interface {
	QueryRow(string, ...any) *sql.Row
}, actor string) (bool, error) {
	var harness string
	if err := q.QueryRow(`SELECT harness FROM sessions WHERE id=?`, actor).Scan(&harness); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if protocol.IsOwner(harness) {
		return true, nil
	}
	var cto string
	if err := q.QueryRow(`SELECT value FROM owner_settings WHERE key='cto_session'`).Scan(&cto); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return cto != "" && actor == cto, nil
}

// withdrawOne marks one message withdrawn and keeps a pending batch in step.
// It does not commit.
func (s *Store) withdrawOne(tx *sql.Tx, m protocol.Message, now int64) error {
	switch m.State {
	case protocol.StateWithdrawn:
		return reject("message %s is already withdrawn", m.ID)
	case protocol.StateDelivered, protocol.StateReplied, protocol.StateAcknowledged:
		return reject("message %s was already delivered to the session; it cannot be withdrawn", m.ID)
	case protocol.StateExpired:
		return reject("message %s expired; it cannot be withdrawn", m.ID)
	case protocol.StateStored, protocol.StateDispatched, protocol.StateHeld, protocol.StateFailed:
	default:
		return reject("message %s is %s; it cannot be withdrawn", m.ID, m.State)
	}
	if m.BatchCarrier != "" {
		if err := s.removeFromBatch(tx, m, now); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`UPDATE messages SET state=?, withdrawn_at=?, held_for_busy=0, held_at=0
		WHERE id=? AND state IN `+withdrawableStates, protocol.StateWithdrawn, now, m.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return reject("message %s is no longer undelivered; it cannot be withdrawn", m.ID)
	}
	if _, err := tx.Exec(`DELETE FROM delivery_retries WHERE message_id=?`, m.ID); err != nil {
		return err
	}
	if m.State == protocol.StateHeld {
		if err := s.closeHeldAsk(tx, m.ID, protocol.AskReasonResolved); err != nil {
			return err
		}
	}
	return nil
}

// removeFromBatch takes a withdrawn original out of its pending batch and
// rebuilds the batch text. A batch whose last original is withdrawn goes too.
func (s *Store) removeFromBatch(tx *sql.Tx, m protocol.Message, now int64) error {
	carrier, ok, err := s.message(tx, "m.id=?", m.BatchCarrier)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	switch carrier.State {
	case protocol.StateStored, protocol.StateDispatched, protocol.StateFailed:
	default:
		return reject("message %s was already delivered to the session; it cannot be withdrawn", m.ID)
	}
	var keep []protocol.Message
	for _, child := range carrier.Batch {
		if child.ID != m.ID {
			keep = append(keep, child)
		}
	}
	if len(keep) == 0 {
		if _, err := tx.Exec(`UPDATE messages SET state=?, withdrawn_at=?, batch_json='' WHERE id=?`,
			protocol.StateWithdrawn, now, carrier.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM delivery_retries WHERE message_id=?`, carrier.ID)
		return err
	}
	kind := keep[0].Type
	urgent := false
	expires := keep[0].ExpiresAt
	for _, child := range keep {
		if child.Type == protocol.TypeRequest {
			kind = protocol.TypeRequest
		}
		urgent = urgent || child.Urgent
		if child.ExpiresAt.Before(expires) {
			expires = child.ExpiresAt
		}
	}
	raw, err := json.Marshal(keep)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE messages SET batch_json=?, body=?, type=?, urgent=?, expires_at=? WHERE id=?`,
		string(raw), busyBatchBody(keep), kind, urgent, expires.UnixNano(), carrier.ID)
	return err
}
