package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
)

// Outcomes lists recorded decisions for an edge, newest first. Empty node
// filters match all nodes; limit is bounded.
func (s *Store) Outcomes(from, to string, limit int) ([]protocol.EdgeOutcome, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT source,target,message_id,reply_id,outcome,reason,redirect_to,recommendation,at FROM graph_outcomes WHERE (?='' OR source=?) AND (?='' OR target=?) ORDER BY at DESC, rowid DESC LIMIT ?`, from, from, to, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.EdgeOutcome{}
	for rows.Next() {
		var o protocol.EdgeOutcome
		if err := rows.Scan(&o.From, &o.To, &o.MessageID, &o.ReplyID, &o.Outcome, &o.Reason, &o.RedirectTo, &o.Recommendation, &o.At); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutcomeInbox returns unresolved rejections and escalations of agent requests.
func (s *Store) OutcomeInbox() ([]protocol.OutcomeInboxEntry, error) {
	rows, err := s.db.Query(`SELECT o.source,o.target,o.message_id,o.reply_id,o.outcome,o.reason,o.redirect_to,o.recommendation,o.at,m.sender,m.recipient,m.body FROM graph_outcomes o JOIN messages m ON m.id=o.message_id JOIN sessions sender ON sender.id=m.sender WHERE o.resolution='' AND o.outcome IN ('reject','escalate') AND sender.harness<>? ORDER BY o.at`, protocol.OwnerHarness)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.OutcomeInboxEntry{}
	for rows.Next() {
		var e protocol.OutcomeInboxEntry
		var body string
		if err = rows.Scan(&e.From, &e.To, &e.MessageID, &e.ReplyID, &e.Outcome, &e.Reason, &e.RedirectTo, &e.Recommendation, &e.At, &e.Sender, &e.Recipient, &body); err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(body, "\n")
		e.Request = protocol.CleanWaitingText(first)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ResolveOutcome records the owner's decision and atomically creates the
// marked request when it is overruled.
func (s *Store) ResolveOutcome(replyID, owner, action string) (protocol.Message, error) {
	var result protocol.Message
	if action != "let_stand" && action != "overrule" {
		return result, reject("invalid resolution")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var harness string
	if err = tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, owner).Scan(&harness); err != nil || !protocol.IsOwner(harness) {
		return result, reject("owner required")
	}
	var requestID, to, body, resolution, outcome, originalHarness string
	err = tx.QueryRow(`SELECT m.id,m.recipient,m.body,o.resolution,o.outcome,s.harness FROM graph_outcomes o JOIN messages m ON m.id=o.message_id JOIN sessions s ON s.id=m.sender WHERE o.reply_id=?`, replyID).Scan(&requestID, &to, &body, &resolution, &outcome, &originalHarness)
	if err != nil || resolution != "" || (outcome != "reject" && outcome != "escalate") || protocol.IsOwner(originalHarness) {
		return result, reject("unknown or resolved outcome")
	}
	now := s.now().UTC()
	msgBody, msgType := body, protocol.TypeRequest
	if action == "let_stand" {
		first, _, _ := strings.Cut(body, "\n")
		msgBody, msgType = "Your decision stands for: "+protocol.CleanWaitingText(first), protocol.TypeNotification
	}
	result = protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: owner, To: to, Type: msgType, Body: msgBody, IdempotencyKey: protocol.NewID("k"), CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored, Overruled: action == "overrule"}
	_, err = tx.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, result.ID, result.ConversationID, "", owner, to, result.Type, msgBody, 0, result.IdempotencyKey, now.UnixNano(), result.ExpiresAt.UnixNano(), result.State)
	if err != nil {
		return result, err
	}
	if action == "overrule" {
		if _, err = tx.Exec(`INSERT INTO outcome_overrules(message_id,reply_id) VALUES(?,?)`, result.ID, replyID); err != nil {
			return result, err
		}
	}
	s.event(tx, "msg.stored", "", owner, result.ID, "")

	if _, err = tx.Exec(`UPDATE graph_outcomes SET resolution=? WHERE reply_id=?`, action, replyID); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.ID != "" {
		result, _, err = s.Message(result.ID)
	}
	return result, err
}
