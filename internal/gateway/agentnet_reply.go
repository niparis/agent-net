package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// agentNetSender is the reserved sender of the messages the gateway writes
// itself. It is not a session.
const agentNetSender = "agent-net"

// chiefAskKeyPrefix starts the idempotency key of a message from agent-net
// about one ask. The ask id follows, then a random part.
const chiefAskKeyPrefix = "chief_ask:"

// askOfAgentNetMessage is the ask a message of agent-net is about, or "".
func askOfAgentNetMessage(m protocol.Message) string {
	if m.From != agentNetSender || !strings.HasPrefix(m.IdempotencyKey, chiefAskKeyPrefix) {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(m.IdempotencyKey, chiefAskKeyPrefix), ":")
	return id
}

// agentNetReply stores the reply or the ack of a session to a message of
// agent-net, inside the message transaction, and commits it. agent-net is not
// a session, so the reply has no one to go to. When the message is about an
// ask, the reply closes the ask: a body is the reason, the same as the chief of
// staff's close, and an ack closes it with no reason. A reply never answers an
// ask that only the owner can decide (route_fixed): that is an error that says
// so. A message that is not about an ask is a notice: nobody reads a reply, and
// the error says that.
//
// The reply is kept as history (state acknowledged). It is never delivered.
func (s *Store) agentNetReply(tx *sql.Tx, p protocol.MsgSend) (protocol.Message, bool, error) {
	var none protocol.Message
	parent, ok, err := s.message(tx, "m.id = ?", p.ParentID)
	if err != nil {
		return none, false, err
	}
	if !ok {
		return none, false, reject("unknown parent message %q", p.ParentID)
	}
	if parent.From != agentNetSender || parent.To != p.Sender {
		return none, false, reject("a reply must come from the parent's recipient and go to its sender")
	}
	askID := askOfAgentNetMessage(parent)
	if askID == "" {
		return none, false, reject("agent-net sent %s as a notice and does not read replies to it", parent.ID)
	}
	if !s.allow(p.Sender) {
		return none, false, reject("rate limit: at most %d messages per minute per session", protocol.RatePerMinute)
	}
	var status, text, aboutKind, aboutID string
	var routeFixed int
	err = tx.QueryRow(`SELECT status,text,about_kind,about_id,route_fixed FROM inbox_items WHERE id=?`, askID).
		Scan(&status, &text, &aboutKind, &aboutID, &routeFixed)
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, reject("ask %s is gone", askID)
	}
	if err != nil {
		return none, false, err
	}
	if routeFixed != 0 {
		return none, false, reject("ask %s is a decision only the owner can answer; a reply cannot close it. If it needs the owner, tell them with agent_owner_ask.", askID)
	}
	now := s.now()
	typ := protocol.TypeReply
	if p.Ack {
		typ = protocol.TypeAck
	}
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: parent.ConversationID, ParentID: parent.ID, Hop: parent.Hop + 1,
		From: p.Sender, To: agentNetSender, Type: typ, Body: p.Body, IdempotencyKey: p.IdempotencyKey,
		CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateAcknowledged}
	if _, err := tx.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop,
		idem_key, created_at, expires_at, state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body, m.Hop,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return none, false, err
	}
	state := protocol.StateReplied
	if p.Ack {
		state = protocol.StateAcknowledged
	}
	if _, err := tx.Exec(`UPDATE messages SET state = ? WHERE id = ? AND state IN (?,?,?)`,
		state, parent.ID, protocol.StateStored, protocol.StateDispatched, protocol.StateDelivered); err != nil {
		return none, false, err
	}
	if status == "open" {
		who := sessionLabel(tx, p.Sender)
		answer := who + " acked"
		if body := strings.TrimSpace(p.Body); body != "" {
			answer = who + ": " + body
		}
		if _, err := tx.Exec(`UPDATE inbox_items SET status='closed', answer=?, answered_at=? WHERE id=? AND status='open'`, answer, now.UnixNano(), askID); err != nil {
			return none, false, err
		}
		if err := s.deskLog(tx, "desk", aboutKind, aboutID, who+" closed ask by reply: "+askFirstLine(text, 120), strings.TrimSpace(p.Body), nil); err != nil {
			return none, false, err
		}
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "type="+m.Type+" to=agent-net ask="+askID)
	if err := tx.Commit(); err != nil {
		return none, false, err
	}
	s.logMessage(m)
	return m, true, nil
}
