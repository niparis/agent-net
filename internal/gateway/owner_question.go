package gateway

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// An owner question shows in the agent's own chat AND follows the route to the
// chief and the Desk (decision 0150). The owner's words, typed to the
// agent-net lead on 2026-11-12: "the question should be in the chat AND follow
// the route". Agents ask with agent_owner_ask and say the question in their
// chat. A turn that ends on a question that went nowhere else is recorded as
// a chief-bound ask, and the turn ends as it is: nothing removes the question
// from the chat or makes the session go on.

// ownerPresentWindow is how long after the owner typed in a session he counts
// as working in it.
const ownerPresentWindow = 30 * time.Minute

// RecordOwnerTyped notes that the owner typed in a session. Viewing or
// attaching does not call it.
func (s *Store) RecordOwnerTyped(session string) error {
	_, err := s.db.Exec(`UPDATE sessions SET owner_typed_at=? WHERE id=?`, s.now().UnixNano(), session)
	return err
}

// ownerInSession tells whether the owner works in the session: he typed there
// in the last ownerPresentWindow, or it is a front door session.
func (s *Store) ownerInSession(q askDB, session string) (bool, error) {
	front, typed, err := s.frontDoorSession(q, session)
	if err != nil || front {
		return front, err
	}
	return typed > s.now().Add(-ownerPresentWindow).UnixNano(), nil
}

// frontDoorSession tells whether the session talks to the owner in its own
// chat with no route: the owner's own sessions, the chief and the CTO. A
// worker asks its caller, never the owner, so it counts too. typed is when the
// owner last typed there.
func (s *Store) frontDoorSession(q askDB, session string) (front bool, typed int64, err error) {
	var harness, worker string
	err = q.QueryRow(`SELECT harness,worker,owner_typed_at FROM sessions WHERE id=?`, session).Scan(&harness, &worker, &typed)
	if errors.Is(err, sql.ErrNoRows) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if protocol.IsOwner(harness) || harness == "cto" || worker != "" {
		return true, typed, nil
	}
	for _, key := range []string{chiefSessionKey, "cto_session"} {
		var id string
		if err := q.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, key).Scan(&id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, 0, err
		}
		if id == session {
			return true, typed, nil
		}
	}
	return false, typed, nil
}

// TurnEndRoutes tells whether a question that ended a session's turn follows
// the route to the chief. It does for every agent session, also while the
// owner types there: the question is in his chat too, and his answer there
// closes the ask. Front door sessions have no route.
func (s *Store) TurnEndRoutes(session string) (bool, error) {
	front, _, err := s.frontDoorSession(s.db, session)
	return !front, err
}

// turnEndQuestion is the ask of a question that ended a turn. The text is cut
// to the limit of an owner ask, not refused: the turn has already ended.
func turnEndQuestion(agent protocol.AgentView, project, name, text string) protocol.InboxItem {
	if r := []rune(text); len(r) > protocol.MaxOwnerAskChars {
		text = string(r[:protocol.MaxOwnerAskChars])
	}
	return protocol.InboxItem{Kind: protocol.AskKindQuestion, SessionID: agent.SessionID, Node: project, Text: text, AskerName: name}
}

// agentOwnerAsk stores an owner ask of an agent session: agent_owner_ask, or a
// question that ended its turn. A team that sends questions to its lead keeps
// it from the owner; otherwise the ask goes to the chief when one is live. A
// text that takes an ask back withdraws it; it never makes a new ask.
func (g *Gateway) agentOwnerAsk(ctx context.Context, st *Store, ask protocol.InboxItem) (map[string]any, error) {
	if id, reason, ok := protocol.AskWithdrawal(ask.Text); ok {
		return g.withdrawAskByText(st, ask.SessionID, id, reason)
	}
	redirect, m, err := st.RedirectOwnerAsk(ask)
	if err != nil {
		return nil, err
	}
	if redirect != nil {
		if m.State != protocol.StateHeld {
			g.dispatch(ctx, m)
		}
		return map[string]any{"item": protocol.InboxItem{ID: redirect.ItemID, Kind: ask.Kind, Status: "open", SessionID: ask.SessionID}, "redirected": redirect}, nil
	}
	item, err := st.CreateInboxItem(ask)
	if err != nil {
		return map[string]any{"item": item}, err
	}
	// A new ask adds to the pending ones. Tell the asker which others are
	// still open so it can name one with replaces.
	others, err := st.OtherOpenAsks(ask.SessionID, item.ID)
	return map[string]any{"item": item, "other_open": others}, err
}

// withdrawAskByText takes back the ask a message names. The message comes from
// the asker session, so the sort checks that it is the asker or a relay and
// writes the same history and log line as any other sort.
func (g *Gateway) withdrawAskByText(st *Store, session, id, reason string) (map[string]any, error) {
	var node string
	if err := st.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, session).Scan(&node); err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "withdrawn by the asker"
	}
	res, _, err := st.SortAsk(node, protocol.AskSortRequest{SessionID: session, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortWithdraw, Reason: reason}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"item": protocol.InboxItem{ID: id, SessionID: session, Status: res.State}, "withdrawn": true, "reason": reason}, nil
}
