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
// "wstart:<start id>". It goes to the chief of staff first (decision 0151):
// the chief answers confirm or picker under the confirm rule, or sends it to
// the CTO, who may answer it too; with no chief it goes to the owner.
// "confirm" starts the worker with the fields, "picker" starts it with the
// picker's choice, anything else ends the start. The answer goes to the
// machine that holds the start as a FrameStartDecision, not to the asking
// session as a message.

// CreateStartOverrideAsk puts the ask on the Desk for a session of the node and
// keeps its facts. It stays open when the owner types in the asking session.
func (s *Store) CreateStartOverrideAsk(node string, p protocol.StartOverrideAsk) (protocol.InboxItem, error) {
	agent, project, name, err := s.askingSession(node, p.Session)
	if err != nil {
		return protocol.InboxItem{}, err
	}
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: protocol.AskKindQuestion, SessionID: agent.SessionID, Node: project, Text: p.Text,
		Recommendation: "let the picker choose", Options: []string{protocol.StartAnswerConfirm, protocol.StartAnswerPicker},
		AskerName: name, FromKind: protocol.AskFromGateway, SourceRef: protocol.StartOverrideRefPrefix + p.StartID,
		WhyYou: "a caller wants to override the worker picker"})
	if err != nil {
		return item, err
	}
	review := 0
	if p.Review {
		review = 1
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO start_override_facts(ask_id,harness,model,effort,level,provider,caller_family,override_family,review) VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, p.Harness, p.Model, p.Effort, p.Level, p.Provider, p.CallerFamily, p.OverrideFamily, review)
	return item, err
}

// overrideFacts reads the facts of a held override ask. An ask from before
// the facts were kept has none.
func overrideFacts(q askDB, id string) (protocol.OverrideFacts, error) {
	var f protocol.OverrideFacts
	err := q.QueryRow(`SELECT harness,model,effort,level,provider,caller_family,override_family,review<>0 FROM start_override_facts WHERE ask_id=?`, id).
		Scan(&f.Harness, &f.Model, &f.Effort, &f.Level, &f.Provider, &f.CallerFamily, &f.OverrideFamily, &f.Review)
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil
	}
	return f, err
}

// confirmOverrideCheck applies the confirm rule for the chief (with the
// context the gateway read) or the CTO (no context: only the family rule).
func confirmOverrideCheck(q askDB, id string, chief bool, c protocol.ConfirmContext) error {
	f, err := overrideFacts(q, id)
	if err != nil {
		return err
	}
	if err := protocol.ConfirmOverride(f, c, chief); err != nil {
		return reject("%v", err)
	}
	return nil
}

// confirmContext reads what the chief's confirm of a held start is checked
// against: the protected providers, the quota reading the start would use on
// the machine that holds it, the worker floors, and the picker's scores and
// level minimums.
func (g *Gateway) confirmContext(ask string) (protocol.ConfirmContext, error) {
	var c protocol.ConfirmContext
	var session string
	if err := g.store.db.QueryRow(`SELECT session_id FROM inbox_items WHERE id=?`, ask).Scan(&session); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, reject("unknown ask %q", ask)
		}
		return c, err
	}
	protected, err := g.store.protectedProviders()
	if err != nil {
		return c, err
	}
	c.Protected = protected.Providers
	if node, err := g.store.SessionNode(session); err == nil {
		c.Quotas = g.ownerQuotas(node)
	}
	if c.Floors, err = g.store.workerFloors(); err != nil {
		return c, err
	}
	if c.Scores, err = g.store.scores(); err != nil {
		return c, err
	}
	c.Minimums, err = g.store.difficultyMinimums()
	return c, err
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
// any other ask. The CTO may confirm under the confirm rule (decision 0151).
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
		if err := confirmOverrideCheck(s.db, id, false, protocol.ConfirmContext{}); err != nil {
			return item, true, err
		}
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
