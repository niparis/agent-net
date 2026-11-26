package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A worker start that a caller held back because it set selection fields
// (protocol/worker_override.go) is one Desk ask. Its source key is
// "wstart:<start id>". It goes straight to the owner: nobody confirms it for
// him. "confirm" starts the worker with the fields, "picker" starts it with
// the picker's choice, and "dismiss" ends it. Any other text is refused with
// the three answers and the start stays held, so free text that names two
// options chooses neither. The answer goes to the machine that holds the start
// as a FrameStartDecision, not to the asking session as a message.

// CreateStartOverrideAsk puts the ask on the Desk for a session of the node and
// keeps its facts on the ask. It stays open when the owner types in the asking session.
func (s *Store) CreateStartOverrideAsk(node string, p protocol.StartOverrideAsk) (protocol.InboxItem, error) {
	agent, project, name, err := s.askingSession(node, p.Session)
	if err != nil {
		return protocol.InboxItem{}, err
	}
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindQuestion, SessionID: agent.SessionID, Node: project, Text: p.Text,
		Recommendation: "let the picker choose", Options: []string{protocol.StartAnswerConfirm, protocol.StartAnswerPicker},
		AskerName: name, FromKind: protocol.AskFromGateway, SourceRef: protocol.StartOverrideRefPrefix + p.StartID,
		NextWho: protocol.AskNextOwner, WhyYou: "a caller wants a worker the picker did not choose"})
	if err != nil {
		return item, err
	}
	raw, err := json.Marshal(p.OverrideFacts)
	if err != nil {
		return item, err
	}
	_, err = s.db.Exec(`UPDATE inbox_items SET override_facts=? WHERE id=?`, string(raw), item.ID)
	return item, err
}

// overrideFacts reads the facts of a held override ask. An ask from before
// the facts were kept has none.
func overrideFacts(q askDB, id string) (protocol.OverrideFacts, error) {
	var f protocol.OverrideFacts
	var raw string
	err := q.QueryRow(`SELECT override_facts FROM inbox_items WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || raw == "" {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal([]byte(raw), &f)
}

// startOverrideRef is the source key of an ask that holds a start, or "".
func (s *Store) startOverrideRef(id string) string {
	var ref string
	if err := s.db.QueryRow(`SELECT source_ref FROM inbox_items WHERE id=? AND source_ref LIKE ?`, id, protocol.StartOverrideRefPrefix+"%").Scan(&ref); err != nil {
		return ""
	}
	return ref
}

// closedStartRefusal is the refusal of an answer to a held start whose ask is
// closed: the start will not run, so an answer cannot bring it back.
func closedStartRefusal(id string) error {
	return reject("ask %s is closed, so its start will not run. The caller can start it again", id)
}

// startAnswerCheck refuses an answer that is not one of the three a held start
// takes. The text is matched whole, never by a word inside it: an answer that
// names two options chooses neither.
func startAnswerCheck(answer string) error {
	switch answer {
	case protocol.StartAnswerConfirm, protocol.StartAnswerPicker, "dismiss":
		return nil
	}
	return reject("a held start takes exactly %q (start it with the fields), %q (start it with the picker's choice) or %q (end it); the answer %q is not one of them",
		protocol.StartAnswerConfirm, protocol.StartAnswerPicker, "dismiss", oneLine(answer, 120))
}

// answerStartAsk answers an open ask that holds a start. handled is false for
// any other ask.
func (s *Store) answerStartAsk(id, answer, actor string, validate ...bool) (item protocol.InboxItem, handled bool, err error) {
	var ref, status string
	err = s.db.QueryRow(`SELECT source_ref, status FROM inbox_items WHERE id=? AND source_ref LIKE ?`, id, protocol.StartOverrideRefPrefix+"%").Scan(&ref, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, true, err
	}
	if status == "closed" {
		return item, true, closedStartRefusal(id)
	}
	if status != "open" {
		return item, false, nil
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || len(answer) > protocol.MaxBodyBytes {
		return item, true, reject("invalid answer")
	}
	if err := startAnswerCheck(answer); err != nil {
		return item, true, err
	}
	if actor != "owner" {
		return item, true, reject("only your owner answers a held worker start")
	}
	if item, err = scanInbox(s.db.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id)); err != nil {
		return item, true, err
	}
	if len(validate) > 0 && validate[0] {
		return item, true, nil
	}
	item.Status, item.Answer, item.AnsweredAt = "answered", answer, s.now().UTC()
	if answer == "dismiss" {
		item.Status, item.Answer = "dismissed", "dismiss"
	}
	return item, true, s.closeAnsweredItem(item, item.Answer)
}

// sendStartDecision tells the machine that holds a start what the owner
// answered. A machine that is offline cannot learn it: the start it held is
// lost, so the caller gets a not-started notice that says why, instead of
// hearing nothing.
func (g *Gateway) sendStartDecision(ctx context.Context, item protocol.InboxItem, ref string) {
	node, err := g.store.SessionNode(item.SessionID)
	if err != nil {
		g.logLine("error", "where", "start decision", "ask", item.ID, "err", err)
		g.startDecisionLost(ctx, item, ref, "the gateway could not find the machine that held this start: start it again")
		return
	}
	c := g.conn(node)
	if c == nil {
		g.logLine("start_decision.offline", "ask", item.ID, "node", node)
		g.startDecisionLost(ctx, item, ref, "the machine that held this start is offline, so the answer could not reach it: start it again")
		return
	}
	frame, err := protocol.EncodeRequest(protocol.FrameStartDecision, protocol.NewID("d"),
		protocol.StartDecision{ItemID: item.ID, Ref: ref, Session: item.SessionID, Answer: item.Answer})
	if err != nil {
		g.logLine("start_decision.encode_failed", "ask", item.ID, "err", err)
		g.startDecisionLost(ctx, item, ref, "the answer could not be sent to the machine that held this start: start it again")
		return
	}
	if err := c.write(ctx, frame); err != nil {
		g.logLine("start_decision.write_failed", "ask", item.ID, "node", node, "err", err)
		g.startDecisionLost(ctx, item, ref, "the answer could not be sent to the machine that held this start: start it again")
	}
}

// startDecisionLost stores agent-net's notice for a start whose answer never
// reached the machine that held it. The start id key makes it one notice, even
// when the machine later reports the same start.
func (g *Gateway) startDecisionLost(ctx context.Context, item protocol.InboxItem, ref, reason string) {
	owner, ok, err := g.store.Agent(item.SessionID)
	if err != nil || !ok || owner.Harness == "bot" || protocol.IsOwner(owner.Harness) {
		return
	}
	id := strings.TrimPrefix(ref, protocol.StartOverrideRefPrefix)
	facts, err := overrideFacts(g.store.db, item.ID)
	if err != nil {
		g.logLine("error", "where", "start decision facts", "ask", item.ID, "err", err)
	}
	n, stored, err := g.store.startReportNotice(item.SessionID, id, protocol.WorkerStartReportText(protocol.StartReportNotStarted,
		oneLine(item.Node, 100), oneLine(facts.Harness, 40), oneLine(id, 40), reason))
	if err != nil {
		g.logLine("error", "where", "start decision notice", "ask", item.ID, "err", err)
		return
	}
	if stored {
		g.dispatch(ctx, n)
	}
}
