package gateway

import (
	"context"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// heldUrgentFor lists the urgent messages the gateway holds for a busy
// session, oldest first. A message with files is left out: the daemon takes
// files only with a normal delivery (decision 0054), so it waits for the turn
// end like any held message.
func (s *Store) heldUrgentFor(sessionID string) ([]protocol.Message, error) {
	rows, err := s.db.Query(msgSelect+` WHERE m.recipient=? AND m.held_for_busy=1 AND m.urgent=1 AND m.state='stored'
		AND m.batch_id='' AND m.expires_at>? ORDER BY m.created_at ASC, m.rowid ASC`, sessionID, s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if len(m.Files) == 0 {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// claimHeld takes a message out of the busy hold. It is true for exactly one
// caller, so the turn-end release and a second hook call cannot send it again.
func (s *Store) claimHeld(id string) bool {
	res, err := s.db.Exec(`UPDATE messages SET held_for_busy=0, held_at=0 WHERE id=? AND state='stored' AND held_for_busy=1`, id)
	if err != nil {
		s.logf("error", "where", "claim held urgent", "msg", id, "err", err)
		return false
	}
	n, err := res.RowsAffected()
	return err == nil && n == 1
}

// takeUrgent serves msg.take_urgent (decision 0180). A session's hook asks, at
// a tool call inside a turn, for the urgent messages held for it. They go
// through the same checks and fields as a normal delivery, are marked
// dispatched, and the daemon shows them to the model and reports delivered.
// Only the node that owns the session may ask.
func (g *Gateway) takeUrgent(ctx context.Context, nodeID, sessionID string) ([]protocol.Message, error) {
	owner, err := g.store.SessionNode(sessionID)
	if err != nil {
		return nil, err
	}
	if owner == "" || owner != nodeID {
		return nil, errors.New("that session is not on this node")
	}
	held, err := g.store.heldUrgentFor(sessionID)
	if err != nil {
		return nil, err
	}
	var out []protocol.Message
	for _, m := range held {
		if !g.store.claimHeld(m.ID) {
			continue
		}
		m.HeldForBusy = false
		prepared, _, ok := g.prepareDelivery(ctx, nodeID, m, true, true)
		if !ok {
			continue
		}
		if err := g.store.MarkDispatched(prepared.ID); err != nil {
			g.logLine("error", "where", "mark dispatched", "err", err)
			continue
		}
		g.store.Event("msg.urgent_taken", "", sessionID, prepared.ID, "")
		g.store.logStep("msg.urgent_taken", prepared.ID)
		out = append(out, prepared)
	}
	return out, nil
}

// notifyUrgentHeld tells the recipient's node that an urgent message waits for
// a busy session. The node then asks for it at the session's next tool call.
// Without this frame the node would ask the gateway on every tool call of
// every session (decision 0180). A node that misses the frame loses only the
// early delivery: the message still goes at the turn end.
func (g *Gateway) notifyUrgentHeld(ctx context.Context, nodeID, sessionID string) {
	g.mu.Lock()
	nc := g.conns[nodeID]
	g.mu.Unlock()
	if nc == nil {
		return
	}
	frame, err := protocol.EncodeRequest(protocol.FrameUrgentHeld, protocol.NewID("d"), protocol.UrgentHeld{SessionID: sessionID})
	if err != nil {
		return
	}
	if err := nc.write(ctx, frame); err != nil {
		g.logLine("error", "where", "urgent held frame", "session", sessionID, "err", err)
	}
}
