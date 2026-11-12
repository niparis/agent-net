package gateway

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// No agent asks the owner in its own chat (decision 0150). Owner questions go
// to the chief with agent_owner_ask. A turn that ends on a question to the
// owner is caught: the question becomes a chief-bound ask, and the session is
// told once to carry on. The owner who typed in the session lately is working
// in it, so a question to him there is normal and nothing is redirected.

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
// in the last ownerPresentWindow, or it is a front door session (the chief or
// the CTO), which talks to him in its own chat.
func (s *Store) ownerInSession(q askDB, session string) (bool, error) {
	var harness, worker string
	var typed int64
	err := q.QueryRow(`SELECT harness,worker,owner_typed_at FROM sessions WHERE id=?`, session).Scan(&harness, &worker, &typed)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if protocol.IsOwner(harness) || harness == "cto" || worker != "" {
		return true, nil
	}
	if typed > s.now().Add(-ownerPresentWindow).UnixNano() {
		return true, nil
	}
	for _, key := range []string{chiefSessionKey, "cto_session"} {
		var id string
		if err := q.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, key).Scan(&id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if id == session {
			return true, nil
		}
	}
	return false, nil
}

// TurnEndRedirect tells whether a question that ends a session's turn goes to
// the chief: the owner is not working in that session. A worker asks its
// caller, never the owner, so it is not redirected here either.
func (s *Store) TurnEndRedirect(session string) (bool, error) {
	in, err := s.ownerInSession(s.db, session)
	return !in, err
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
// it from the owner; otherwise the ask goes to the chief when one is live.
func (g *Gateway) agentOwnerAsk(ctx context.Context, st *Store, ask protocol.InboxItem) (map[string]any, error) {
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
