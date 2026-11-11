package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// undeliveredKeyPrefix marks agent-net's notice that a message reached a final
// failed state. The key is unique per sender, so each message gets at most one
// notice, also across gateway restarts.
const undeliveredKeyPrefix = "undelivered:"

// isUndeliveredNotice tells whether the reserved sender's message is a notice
// about a failed message. It is delivered through the normal queue and is not
// a reminder.
func isUndeliveredNotice(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, undeliveredKeyPrefix)
}

// needsReminderCheck tells whether a message is a reminder from agent-net,
// which is only delivered while the request it is about is still open.
func needsReminderCheck(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, "reminder:")
}

// undeliveredNotice stores one notice to the sender of m, which has reached a
// final failed state: "message to X was not delivered: reason". When the
// message may have arrived, it says "may not have arrived" instead. No notice
// is stored for agent-net's own messages (so a failed notice never makes
// another), for the owner and connected apps (the TUI and the app show the
// state), or when the notice was stored already. The notice waits in the
// queue like any message while the sender is offline.
func (s *Store) undeliveredNotice(tx *sql.Tx, m protocol.Message, reason string, uncertain bool) (protocol.Message, bool, error) {
	var none protocol.Message
	if m.From == "agent-net" {
		return none, false, nil
	}
	var harness string
	err := tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, m.From).Scan(&harness)
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}
	if err != nil {
		return none, false, err
	}
	if harness == "bot" || protocol.IsOwner(harness) {
		return none, false, nil
	}
	if paused, err := s.nudgePaused(tx, "undelivered", m.From); err != nil {
		return none, false, err
	} else if paused {
		return none, false, nil
	}
	var name string
	if err = tx.QueryRow(`SELECT COALESCE(NULLIF(p.name,''),NULLIF(r.name,''),?) FROM (SELECT ? AS id) x
		LEFT JOIN sessions r ON r.id=x.id LEFT JOIN profiles p ON p.session_id=x.id`, m.To, m.To).Scan(&name); err != nil {
		return none, false, err
	}
	verb := "was not delivered"
	if uncertain {
		verb = "may not have arrived"
	}
	body := fmt.Sprintf("[agent-net] message to %s %s: %s.\nMessage %s: \"%s\"\nThis is a notice from agent-net. No reply is needed.",
		protocol.NeutralizeMarkers(html.EscapeString(protocol.CleanWaitingText(name))), verb,
		protocol.NeutralizeMarkers(conversationLine(strings.TrimSuffix(reason, "."))), m.ID,
		protocol.NeutralizeMarkers(conversationLine(m.Body)))
	now := s.now()
	n := protocol.Message{ID: protocol.NewID("m"), ConversationID: m.ConversationID, ParentID: m.ID,
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: m.From, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: undeliveredKeyPrefix + m.ID,
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
	s.event(tx, "msg.stored", "", n.From, n.ID, "type=notification by=agent-net undelivered="+m.ID)
	return n, true, nil
}

// failureNotice is the notice for a delivery report that leaves a message
// failed for good. A detail that says the message may have arrived gives the
// "may not have arrived" form.
func (s *Store) failureNotice(tx *sql.Tx, id, detail string) (protocol.Message, bool, error) {
	m, ok, err := s.message(tx, "m.id=?", id)
	if err != nil || !ok {
		return protocol.Message{}, false, err
	}
	reason := strings.TrimSpace(detail)
	uncertain := strings.Contains(strings.ToLower(reason), "may have arrived")
	if uncertain {
		reason = strings.TrimSpace(strings.TrimPrefix(reason, "may have arrived:"))
	}
	if reason == "" {
		reason = "the receiving machine reported a failure"
	}
	return s.undeliveredNotice(tx, m, reason, uncertain)
}

// expiryNotice is the notice for a message that expired before any delivery
// was confirmed. A message still dispatched may have arrived without a report.
func (s *Store) expiryNotice(tx *sql.Tx, m protocol.Message) (protocol.Message, bool, error) {
	var last string
	err := tx.QueryRow(`SELECT detail FROM deliveries WHERE message_id=? AND state='failed' ORDER BY rowid DESC LIMIT 1`, m.ID).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.Message{}, false, err
	}
	uncertain := m.State == protocol.StateDispatched
	reason := "expired after " + ttlText(m.ExpiresAt.Sub(m.CreatedAt))
	if uncertain {
		reason = "expired with no delivery report"
	}
	if last = strings.TrimSpace(last); last != "" {
		reason += "; last error: " + last
	}
	return s.undeliveredNotice(tx, m, reason, uncertain)
}

func ttlText(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		h := int(d / time.Hour)
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	return d.Round(time.Second).String()
}
