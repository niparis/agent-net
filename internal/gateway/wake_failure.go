package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// wakeFailedKeyPrefix marks agent-net's notice that a wake failed. The key is
// unique per attempt and sender, so each sender hears about one attempt once.
const wakeFailedKeyPrefix = "wake-failed:"

// wakeAllowed tells whether agent-net may ask a machine to wake an offline
// session of this harness for a message. A wakeable harness (wakeHarness:
// Claude in terminal or background mode, Codex, OpenCode; not Pi; never
// workers) is woken by a message when wake_on_message is on (the default).
// Autonomy wake still allows it for backward compatibility; loop ticks still
// count; step messages keep their own rule.
func (g *Gateway) wakeAllowed(a protocol.AgentView, autonomy bool) bool {
	if !wakeHarness(a.Harness) || a.Worker != nil {
		return false
	}
	if g.wakeOnMessage(a) {
		return true
	}
	return autonomy
}

// wakeStatusLine says what a queued message to an offline session of a wakeable
// harness means for its wake. A wake under way is reported as a wake, not as the
// failure of an earlier attempt: started means this message started one, and a
// wake that an earlier message started may still be under way. It is empty when
// the session has no reason to report.
func (g *Gateway) wakeStatusLine(a protocol.AgentView, started bool) string {
	name, err := wakeNoticeName(g.store.db, a.SessionID)
	if err != nil {
		name = a.SessionID
	}
	if !g.wakeAllowed(a, g.autonomyWake(a)) {
		return "queued; " + name + " is offline and wake is off for this agent"
	}
	if started || g.wakeInFlight(a.SessionID) {
		return "queued; " + name + " is offline, waking it"
	}
	if reason, ok := g.store.wakeFailure(a.SessionID); ok {
		return "queued; " + name + " is offline and its last wake failed: " + reason
	}
	return ""
}

// wakeFailed records a wake that the machine answered with an error, so that a
// gateway restart does not retry it, and tells each sender with a queued
// message once. A connection that dropped gave no answer: that is not recorded,
// so the reconnect asks again as before. A wake that was cancelled (the gateway
// is stopping) or that hit its deadline gave no answer either, so it is not
// recorded. Only an error the machine sent is a refusal.
func (g *Gateway) wakeFailed(ctx context.Context, session, attempt string, err error) {
	if errors.Is(err, errNodeGone) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	notices, serr := g.store.failWake(session, attempt, err.Error())
	if serr != nil {
		g.logLine("error", "where", "record session wake failure", "session", session, "err", serr)
		return
	}
	for _, n := range notices {
		g.dispatch(ctx, n)
	}
}

// failWake stores the failure of a wake and one agent-net notice for each
// sender with a stored message to the session. It returns the notices.
func (s *Store) failWake(session, attempt, reason string) ([]protocol.Message, error) {
	reason = strings.TrimSpace(reason)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now()
	if _, err := tx.Exec(`INSERT INTO session_wake_failures(session_id,failed_at,reason) VALUES(?,?,?)
 ON CONFLICT(session_id) DO UPDATE SET failed_at=excluded.failed_at, reason=excluded.reason`, session, now.UnixNano(), reason); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT DISTINCT sender FROM messages WHERE recipient=? AND state='stored' AND expires_at>?`, session, now.UnixNano())
	if err != nil {
		return nil, err
	}
	var senders []string
	for rows.Next() {
		var sender string
		if err := rows.Scan(&sender); err != nil {
			rows.Close()
			return nil, err
		}
		senders = append(senders, sender)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []protocol.Message
	for _, sender := range senders {
		n, ok, err := s.wakeFailureNotice(tx, sender, session, attempt, reason)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, n)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// wakeFailureNotice stores the notice to one sender. No notice goes to
// agent-net, to bots, or to the owner and connected apps, which show the state
// themselves. The notice waits in the queue like any message.
func (s *Store) wakeFailureNotice(tx *sql.Tx, to, session, attempt, reason string) (protocol.Message, bool, error) {
	var none protocol.Message
	if to == "agent-net" {
		return none, false, nil
	}
	var harness string
	err := tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, to).Scan(&harness)
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}
	if err != nil {
		return none, false, err
	}
	if harness == "bot" || protocol.IsOwner(harness) {
		return none, false, nil
	}
	if paused, err := s.nudgePaused(tx, "wake_failed", to); err != nil {
		return none, false, err
	} else if paused {
		return none, false, nil
	}
	name, err := wakeNoticeName(tx, session)
	if err != nil {
		return none, false, err
	}
	body := fmt.Sprintf("[agent-net] %s is offline and could not be woken: %s.\nYour message to it stays queued and is delivered when it starts again. This is a notice from agent-net. No reply is needed.",
		protocol.NeutralizeMarkers(name), protocol.NeutralizeMarkers(reason))
	now := s.now()
	n := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: to, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: wakeFailedKeyPrefix + attempt + ":" + to,
		CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	res, err := tx.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, n.ID, n.ConversationID, n.ParentID, n.From, n.To, n.Type, n.Body,
		n.IdempotencyKey, n.CreatedAt.UnixNano(), n.ExpiresAt.UnixNano(), n.State)
	if err != nil {
		return none, false, err
	}
	if c, err := res.RowsAffected(); err != nil || c == 0 {
		return none, false, err
	}
	s.event(tx, "msg.stored", "", n.From, n.ID, "type=notification by=agent-net wake_failed="+session)
	return n, true, nil
}

// wakeNoticeName is the name a session has in notices: its profile name, else
// its session name, else its ID.
func wakeNoticeName(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, session string) (string, error) {
	var name string
	err := q.QueryRow(`SELECT COALESCE(NULLIF(p.name,''),NULLIF(r.name,''),?) FROM (SELECT ? AS id) x
		LEFT JOIN sessions r ON r.id=x.id LEFT JOIN profiles p ON p.session_id=x.id`, session, session).Scan(&name)
	return name, err
}

// wakeFailure returns the reason of a session's last failed wake, if it has
// not been cleared by a wake that worked.
func (s *Store) wakeFailure(session string) (string, bool) {
	var reason string
	if err := s.db.QueryRow(`SELECT reason FROM session_wake_failures WHERE session_id=?`, session).Scan(&reason); err != nil {
		return "", false
	}
	return reason, true
}

// clearWakeFailure forgets a session's failed wake after a wake worked.
func (s *Store) clearWakeFailure(session string) error {
	_, err := s.db.Exec(`DELETE FROM session_wake_failures WHERE session_id=?`, session)
	return err
}
