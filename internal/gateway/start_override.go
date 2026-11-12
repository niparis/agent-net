package gateway

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker start that an agent held back because it set selection fields
// (protocol/worker_override.go) is one Desk ask. Its source key is
// "wstart:<start id>". Only the owner answers it: "confirm" starts the worker
// with the fields, "picker" starts it with the picker's choice, anything else
// ends the start. The answer goes to the machine that holds the start as a
// FrameStartDecision, not to the asking session as a message.

// CreateStartOverrideAsk puts the ask on the Desk for a session of the node.
// The owner answers it himself: it is routed to him, never to a chief of staff
// or a team lead, and it stays open when he types in the asking session.
func (s *Store) CreateStartOverrideAsk(node, session, startID, text string) (protocol.InboxItem, error) {
	agent, project, name, err := s.askingSession(node, session)
	if err != nil {
		return protocol.InboxItem{}, err
	}
	return s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindQuestion, SessionID: agent.SessionID, Node: project, Text: text,
		Recommendation: "let the picker choose", Options: []string{protocol.StartAnswerConfirm, protocol.StartAnswerPicker},
		AskerName: name, FromKind: protocol.AskFromGateway, SourceRef: protocol.StartOverrideRefPrefix + startID,
		NextWho: protocol.AskNextOwner, WhyYou: "only you can override the worker picker"})
}

// startOverrideRef is the source key of an ask that holds a start, or "".
func (s *Store) startOverrideRef(id string) string {
	var ref string
	if err := s.db.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=? AND source_ref LIKE ?`, id, protocol.StartOverrideRefPrefix+"%").Scan(&ref); err != nil {
		return ""
	}
	return ref
}

// answerStartAsk answers an open ask that holds a start. handled is false for
// any other ask. The CTO is not a human: it may choose the picker, but only the
// owner confirms an override.
func (s *Store) answerStartAsk(id, answer, actor string, validate ...bool) (item protocol.InboxItem, handled bool, err error) {
	var ref string
	err = s.db.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=? AND status='open' AND source_ref LIKE ?`, id, protocol.StartOverrideRefPrefix+"%").Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, true, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || len(answer) > protocol.MaxBodyBytes {
		return item, true, reject("invalid answer")
	}
	if actor == "cto" && answer == protocol.StartAnswerConfirm {
		return item, true, reject("only the owner can confirm that a caller overrides the worker picker; the CTO can answer picker")
	}
	if item, err = scanInbox(s.db.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id)); err != nil {
		return item, true, err
	}
	if len(validate) > 0 && validate[0] {
		return item, true, nil
	}
	item.Status, item.Answer, item.AnsweredAt = "answered", answer, s.now().UTC()
	if answer == "dismiss" || answer == "seen" {
		item.Status, item.Answer = "dismissed", "dismiss"
	}
	return item, true, s.closeAnsweredItem(item, item.Answer)
}

// sendStartDecision tells the machine that holds a start what the owner
// answered. A machine that is offline learns nothing: the start it held is
// lost with its restart in any case.
func (g *Gateway) sendStartDecision(ctx context.Context, item protocol.InboxItem, ref string) {
	node, err := g.store.SessionNode(item.SessionID)
	if err != nil {
		g.logLine("error", "where", "start decision", "ask", item.ID, "err", err)
		return
	}
	c := g.conn(node)
	if c == nil {
		g.logLine("start_decision.offline", "ask", item.ID, "node", node)
		return
	}
	frame, err := protocol.EncodeRequest(protocol.FrameStartDecision, protocol.NewID("d"),
		protocol.StartDecision{ItemID: item.ID, Ref: ref, Session: item.SessionID, Answer: item.Answer})
	if err == nil {
		_ = c.write(ctx, frame)
	}
}
