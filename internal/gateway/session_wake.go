package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
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
	res, err := tx.Exec(`UPDATE sessions SET wake_attempted_at=? WHERE id=? AND wake_attempted_at<=?`, now.UnixNano(), session, now.Add(-wakeBackoff).UnixNano())
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
func wakeHarness(h string) bool { return protocol.WakeHarness(h) }

// roleSession tells whether a session holds a role other agents rely on: the
// chief of staff or a project's merger. It is woken whatever its agent's wake
// and autonomy settings say, because those settings are made for coding
// sessions (decision 0171). The CTO is not here: the daemon keeps it running
// and delivers to it directly. A lookup error is false.
func (s *Store) roleSession(id string) bool {
	var held bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM gateway_state WHERE key=? AND value=?)
 OR EXISTS(SELECT 1 FROM project_mergers WHERE session_id=?)`, chiefSessionKey, id, id).Scan(&held)
	return err == nil && held
}

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
	if allowed, err := g.store.allowNudgeDelivery(m); err != nil || !allowed {
		return false
	}
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

// waitingReason says why an offline recipient will not be woken for a queued
// message, so its sender is told at once and not at expiry. It is empty when a
// wake is under way or can still start (the machine is away, or a wake ran
// moments ago), because the message then waits for a reason the sender need
// not act on.
func (g *Gateway) waitingReason(a protocol.AgentView, m protocol.Message) string {
	if a.Online || m.State != protocol.StateStored || !m.ExpiresAt.After(g.store.now()) {
		return ""
	}
	switch {
	case a.Worker != nil:
		return "it is an ended worker, and a message does not start a worker. It is delivered only if the worker is started again"
	case a.Harness == "pi" && g.autonomyWake(a):
		return "it is offline, and Pi sessions are not woken by messages. It is delivered when the session starts again"
	case !wakeHarness(a.Harness):
		return ""
	case !g.wakeAllowed(a, isLoopMessage(m) || g.autonomyWake(a)):
		return "it is offline, and wake is off for this agent. It is delivered when the session starts again"
	case g.wakeInFlight(a.SessionID):
		return ""
	}
	if reason, ok := g.store.wakeFailure(a.SessionID); ok {
		return "it is offline, and its last wake failed: " + strings.TrimSuffix(reason, ".") + ". It is delivered when the session starts again. The next message sent after " + strconv.Itoa(int(wakeBackoff.Minutes())) + " minutes asks for a wake again"
	}
	return ""
}

// tellWaiting gives the sender of m the early notice when waitingReason has one.
func (g *Gateway) tellWaiting(ctx context.Context, a protocol.AgentView, m protocol.Message) {
	reason := g.waitingReason(a, m)
	if reason == "" {
		return
	}
	notice, ok, err := g.store.WaitingNotice(m, reason)
	if err != nil {
		g.logLine("error", "where", "waiting notice", "msg", m.ID, "err", err)
		return
	}
	if ok {
		g.dispatch(ctx, notice)
	}
}

// onlineWait is how long a wake that the machine accepted may take to bring the
// session online (decision 0099). A machine can accept a wake and start
// nothing, for instance when it holds the session live already, so the
// gateway checks the result instead of trusting the answer.
//
// A cold Claude or Codex start on a loaded machine can take more than 30
// seconds, so the wait is 2 minutes. A session that comes online later clears
// the failure this wait records (see upsertSession).
const onlineWait = 2 * time.Minute

// awaitOnline waits until the session is online, up to the gateway's online
// wait. It returns an error that says so when it is not. A read error is not a
// failed wake and returns nil.
func (g *Gateway) awaitOnline(ctx context.Context, session string) error {
	wait := g.wakeOnlineWait
	if wait <= 0 {
		wait = onlineWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		a, ok, err := g.store.Agent(session)
		if err != nil || !ok || a.Online {
			return nil
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("the machine accepted the wake, but the session did not come online within %s", wait.Round(time.Second))
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
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
	// A replaced session was stopped on purpose: its work and its messages
	// belong to the session that replaced it.
	if !wakeHarness(a.Harness) || a.Worker != nil || a.Online || a.ReplacedBy != "" {
		return false
	}
	// A role session is woken whatever the autonomy setting. The frame carries
	// the autonomy, which is what lets the machine wake a Codex or OpenCode one.
	autonomy := loop || g.autonomyWake(a) || g.store.roleSession(a.SessionID)
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
	// The claim is removed when the machine has answered, on every path after
	// this point, and not later: a wake is over when the machine answers. The wait
	// for the session to come online (awaitOnline) runs after the claim is gone,
	// so it is no wake in flight: a message that arrives meanwhile neither joins
	// it nor holds it. The claim is removed after the answer is recorded, so a
	// receipt written while the claim is set can only say that a wake is under way.
	go func() {
		var once sync.Once
		release := func() {
			once.Do(func() {
				g.wakeMu.Lock()
				if g.wakeClaims[a.SessionID].token == token {
					delete(g.wakeClaims, a.SessionID)
				}
				g.wakeMu.Unlock()
			})
		}
		defer release()
		cctx, cancel := context.WithTimeout(wctx, wakeTimeout)
		defer cancel()
		frame := map[string]string{"session_id": a.SessionID, "token": token}
		if autonomy || fromMessage {
			frame["autonomy"] = protocol.AutonomyWake
		}
		raw, err := target.call(cctx, protocol.FrameSessionWake, frame)
		if err != nil {
			// An untrusted folder can stop the wake before it starts. The owner
			// gets the same one-click trust ask a start would raise.
			if strings.Contains(strings.ToLower(err.Error()), "trust") && a.Cwd != "" {
				g.raiseTrustAsk(a.SessionID, a.NodeID, a.Hostname, a.Cwd, a.Profile.Project, a.SessionID, nil)
			}
			g.logLine("error", "where", "session wake", "session", a.SessionID, "err", err)
			g.wakeFailed(wctx, a.SessionID, token, err)
			return
		}
		release()
		var res protocol.StartedSession
		if json.Unmarshal(raw, &res) == nil && res.Status == "needs_trust" && res.Path != "" {
			g.raiseTrustAsk(a.SessionID, a.NodeID, a.Hostname, res.Path, res.Project, a.SessionID, nil)
		}
		if err := g.awaitOnline(wctx, a.SessionID); err != nil {
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
 AND r.wake_failed_at=0
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
	// dispatch moves a message for a replaced session to its replacement first.
	for _, m := range pending {
		g.dispatch(ctx, m)
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
