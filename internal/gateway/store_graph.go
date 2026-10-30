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
