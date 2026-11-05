package gateway

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type wakeClaim struct{ token, node, native string }
type wakeRebind struct {
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	NativeID  string `json:"native_id"`
}

const wakeBackoff = 10 * time.Minute
const wakeTimeout = 2 * time.Minute

// claimWake atomically limits attempts and extends pending messages past the
// bounded wake. It uses the same database transaction as expiry, so expiry
// cannot win after a wake has been claimed. A crash leaves a bounded extension.
func (s *Store) claimWake(session string) (bool, error) {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO retired_session_wakes(session_id,attempted_at) VALUES(?,?)
 ON CONFLICT(session_id) DO UPDATE SET attempted_at=excluded.attempted_at
 WHERE retired_session_wakes.attempted_at<=?`, session, now.UnixNano(), now.Add(-wakeBackoff).UnixNano())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	// Keep all already queued messages available while the daemon is starting,
	// and allow time for normal registration and dispatch after it answers.
	_, err = tx.Exec(`UPDATE messages SET expires_at=MAX(expires_at,?) WHERE recipient=?
 AND state IN ('stored','dispatched','failed') AND expires_at>?`, now.Add(wakeTimeout+time.Minute).UnixNano(), session, now.UnixNano())
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (g *Gateway) wakeForMessage(ctx context.Context, a protocol.AgentView, m protocol.Message) {
	if a.Harness != "claude" || a.Worker != nil || a.Online || m.State != protocol.StateStored || !m.ExpiresAt.After(g.store.now()) {
		return
	}
	if m.From == "agent-net" && len(m.IdempotencyKey) >= 5 && m.IdempotencyKey[:5] == "step_" {
		current, err := g.store.stepMessageCurrent(m.ID)
		if err != nil || !current {
			return
		}
	}
	g.mu.Lock()
	target := g.conns[a.NodeID]
	g.mu.Unlock()
	if target == nil {
		return
	}
	claimed, err := g.store.claimWake(a.SessionID)
	if err != nil {
		g.logLine("error", "where", "claim session wake", "session", a.SessionID, "err", err)
		return
	}
	if !claimed {
		return
	}
	token := protocol.NewID("wake")
	g.wakeMu.Lock()
	if g.wakeClaims == nil {
		g.wakeClaims = make(map[string]wakeClaim)
	}
	g.wakeClaims[a.SessionID] = wakeClaim{token: token, node: a.NodeID, native: a.NativeID}
	g.wakeMu.Unlock()
	go func() {
		defer func() {
			g.wakeMu.Lock()
			if g.wakeClaims[a.SessionID].token == token {
				delete(g.wakeClaims, a.SessionID)
			}
			g.wakeMu.Unlock()
		}()
		cctx, cancel := context.WithTimeout(ctx, wakeTimeout)
		defer cancel()
		_, err := target.call(cctx, protocol.FrameSessionWake, map[string]string{"session_id": a.SessionID, "token": token})
		if err != nil {
			g.logLine("error", "where", "session wake", "session", a.SessionID, "err", err)
		}
	}()
}

// On reconnect, queued messages can have arrived while the machine was away.
func (g *Gateway) wakePending(ctx context.Context, node string) {
	rows, err := g.store.db.Query(msgSelect+` JOIN sessions r ON r.id=m.recipient
 WHERE r.node_id=? AND r.online=0 AND r.harness='claude' AND r.worker=''
 AND m.state='stored' AND m.expires_at>? ORDER BY m.created_at,m.rowid`, node, g.store.now().UnixNano())
	if err != nil {
		g.logLine("error", "where", "wake pending", "err", err)
		return
	}
	var pending []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			g.logLine("error", "where", "wake pending", "err", err)
			return
		}
		pending = append(pending, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		g.logLine("error", "where", "wake pending", "err", err)
		return
	}
	for _, m := range pending {
		a, ok, err := g.store.Agent(m.To)
		if err == nil && ok {
			g.wakeForMessage(ctx, a, m)
		}
	}
}

// A resume can produce a copy with a different native ID. While the daemon
// still holds discovery's lock, preserve the gateway identity so queued work,
// merger ownership and trust stay with the resumed conversation. Only the
// machine and the specific in-flight wake can request the rebind.
func (g *Gateway) rebindWake(node string, p wakeRebind) error {
	g.wakeMu.Lock()
	defer g.wakeMu.Unlock()
	claim, ok := g.wakeClaims[p.SessionID]
	if !ok || claim.node != node || claim.token != p.Token || p.NativeID == "" || p.NativeID == claim.native {
		return reject("unknown session wake")
	}
	res, err := g.store.db.Exec(`UPDATE sessions SET native_id=? WHERE id=? AND node_id=? AND native_id=?
 AND harness='claude' AND online=0 AND worker='' AND NOT EXISTS
 (SELECT 1 FROM sessions WHERE node_id=? AND harness='claude' AND native_id=?)`,
		p.NativeID, p.SessionID, node, claim.native, node, p.NativeID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return reject("session wake changed before rebind")
	}
	return nil
}
