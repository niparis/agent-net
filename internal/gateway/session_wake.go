package gateway

import (
	"context"
	"strings"
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

// wakeHarness tells whether agent-net can resume an offline session of this
// harness at all. Pi cannot: its rpc mode does not open an existing session.
func wakeHarness(h string) bool { return h == "claude" || h == "codex" || h == "opencode" }

// autonomyWake tells whether the agent's autonomy rule lets agent-net wake it
// for a message. A lookup error is off.
func (g *Gateway) autonomyWake(a protocol.AgentView) bool {
	mode, err := g.store.agentAutonomy(a)
	return err == nil && mode == protocol.AutonomyWake
}

func (g *Gateway) wakeOnMessage(a protocol.AgentView) bool {
	return g.store.agentWakeOnMessage(a)
}

// wakeReceiptNote adds to the sender's routing note when a message waits for an
// offline session that is not woken: a Pi session that autonomy would wake, or
// any wakeable session when wake is off, when it is being woken, or when its
// last wake failed. started says that dispatching m started a wake of the session.
func (g *Gateway) wakeReceiptNote(m protocol.Message, route string, started bool) string {
	if m.State != protocol.StateStored {
		return route
	}
	a, ok, err := g.store.Agent(m.To)
	if err != nil || !ok || a.Worker != nil || a.Online {
		return route
	}
	line := ""
	switch {
	case a.Harness == "pi" && g.autonomyWake(a):
		line = "Pi sessions are not woken by messages"
	case wakeHarness(a.Harness):
		line = g.wakeStatusLine(a, started)
	}
	if line == "" {
		return route
	}
	if route == "" {
		return line
	}
	return route + " " + line
}

// wakeForMessage wakes an offline session for m when the wake rules allow it. It
// reports whether it started a wake of the session.
func (g *Gateway) wakeForMessage(ctx context.Context, a protocol.AgentView, m protocol.Message) bool {
	if !wakeHarness(a.Harness) || a.Worker != nil || a.Online || m.State != protocol.StateStored || !m.ExpiresAt.After(g.store.now()) {
		return false
	}
	if m.From == "agent-net" && len(m.IdempotencyKey) >= 5 && m.IdempotencyKey[:5] == "step_" {
		current, err := g.store.stepMessageCurrent(m.ID)
		if err != nil || !current {
			return false
		}
	} else if !g.wakeAllowed(a, isLoopMessage(m) || g.autonomyWake(a)) {
		// A queued message wakes only what wakeAllowed lets it wake. The step
		// messages below keep the rule of their ready task (wakeSession). A
		// standing loop tick counts as an autonomy wake (the owner set the loop).
		return false
	}
	fromMessage := g.wakeAllowed(a, isLoopMessage(m) || g.autonomyWake(a))
	return g.wakeSessionAs(ctx, a, isLoopMessage(m), fromMessage)
}

// isLoopMessage tells whether the reserved sender's message is a standing
// loop tick. The owner set the loop, so it may wake a session the way an
// autonomy wake does.
func isLoopMessage(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, "loop:")
}

// wakeSession is shared by queued step messages and explicitly assigned ready
// step tasks. The Claude background wake does not depend on autonomy. Every
// other wake does: an interactive Claude session, Codex and OpenCode. The
// receiving daemon makes the final safety checks.
func (g *Gateway) wakeSession(ctx context.Context, a protocol.AgentView) {
	g.wakeSessionAs(ctx, a, false, false)
}

// wakeSessionAs is wakeSession. loop says the wake is a standing loop tick,
// which does not need the autonomy rule. fromMessage says the wake was
// permitted by wakeForMessage (e.g. by wake_on_message). It reports whether it
// started a wake: the claim is taken and the call to the machine is under way.
func (g *Gateway) wakeSessionAs(ctx context.Context, a protocol.AgentView, loop bool, fromMessage bool) bool {
	if !wakeHarness(a.Harness) || a.Worker != nil || a.Online {
		return false
	}
	autonomy := loop || g.autonomyWake(a)
	if !fromMessage {
		if !autonomy && (a.Harness != "claude" || a.Mode == protocol.ModeForeground) {
			return false
		}
	}
	g.mu.Lock()
	target := g.conns[a.NodeID]
	g.mu.Unlock()
	if target == nil {
		return false
	}
	claimed, err := g.store.claimWake(a.SessionID)
	if err != nil {
		g.logLine("error", "where", "claim session wake", "session", a.SessionID, "err", err)
		return false
	}
	if !claimed {
		return false
	}
	token := protocol.NewID("wake")
	g.wakeMu.Lock()
	if g.wakeClaims == nil {
		g.wakeClaims = make(map[string]wakeClaim)
	}
	g.wakeClaims[a.SessionID] = wakeClaim{token: token, node: a.NodeID, native: a.NativeID}
	g.wakeMu.Unlock()
	// The wake outlives the caller. A message from a bot or an app arrives on an
	// HTTP request whose context ends when the handler returns, and the machine
	// may still be answering. The wake keeps the caller's values, and wakeTimeout
	// still bounds the call. Every caller of dispatch gets this, so none of them
	// needs its own copy.
	wctx := context.WithoutCancel(ctx)
	// The claim is removed when the call returns, on every path after this point.
	// It is removed after the answer is recorded, so a receipt written while the
	// claim is set can only say that a wake is under way.
	go func() {
		defer func() {
			g.wakeMu.Lock()
			if g.wakeClaims[a.SessionID].token == token {
				delete(g.wakeClaims, a.SessionID)
			}
			g.wakeMu.Unlock()
		}()
		cctx, cancel := context.WithTimeout(wctx, wakeTimeout)
		defer cancel()
		frame := map[string]string{"session_id": a.SessionID, "token": token}
		if autonomy || fromMessage {
			frame["autonomy"] = protocol.AutonomyWake
		}
		_, err := target.call(cctx, protocol.FrameSessionWake, frame)
		if err != nil {
			g.logLine("error", "where", "session wake", "session", a.SessionID, "err", err)
			g.wakeFailed(wctx, a.SessionID, token, err)
			return
		}
		if err := g.store.clearWakeFailure(a.SessionID); err != nil {
			g.logLine("error", "where", "clear session wake failure", "session", a.SessionID, "err", err)
		}
	}()
	return true
}

// wakeInFlight tells whether a wake of the session is under way, started by any
// message: claimed, and not yet answered by the machine. A receipt uses it for a
// message that joins a wake another message started.
func (g *Gateway) wakeInFlight(session string) bool {
	g.wakeMu.Lock()
	defer g.wakeMu.Unlock()
	_, ok := g.wakeClaims[session]
	return ok
}

// On reconnect, queued messages can have arrived while the machine was away.
// A session whose last wake the machine refused is not asked again here: only a
// new message asks (see wakeFailed).
func (g *Gateway) wakePending(ctx context.Context, node string) {
	rows, err := g.store.db.Query(msgSelect+` JOIN sessions r ON r.id=m.recipient
 WHERE r.node_id=? AND r.online=0 AND r.harness IN ('claude','codex','opencode') AND r.worker=''
 AND NOT EXISTS (SELECT 1 FROM session_wake_failures f WHERE f.session_id=m.recipient)
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
