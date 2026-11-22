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

// needsReminderCheck tells whether a message is a reminder from agent-net,
// which is only delivered while the request it is about is still open.
func needsReminderCheck(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, "reminder:")
}

// undeliveredNotice stores one notice to the sender of m, which has reached a
// final failed state: "message to X was not delivered: reason". When the
// message may have arrived, it says "may not have arrived" instead. No notice
// is stored for agent-net's own messages (so a failed notice never makes
// another), for an ended or offline worker, for the owner and connected apps
// (the TUI and the app show the state), or when the notice was stored already.
// The notice waits in the queue like any message while the sender is offline.
//
// waiting is the early form: the message is still queued, and the notice says
// why it waits ("is waiting") instead of that it failed. It is also sent for an
// ended worker, because nothing will wake a worker for a message. The key is
// the same, so the notice at expiry is not a second one.
func (s *Store) undeliveredNotice(tx *sql.Tx, m protocol.Message, reason string, uncertain, waiting bool) (protocol.Message, bool, error) {
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
	var ended int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=? AND worker<>'' AND (worker_run_active=0 OR online=0)`, m.To).Scan(&ended); err != nil {
		return none, false, err
	}
	if ended != 0 && !waiting {
		return none, false, nil
	}
	if paused, err := s.nudgePaused(tx, "undelivered", m.From); err != nil {
		return none, false, err
	} else if paused {
		return none, false, nil
	}
	var name string
	if err = tx.QueryRow(`SELECT COALESCE((SELECT NULLIF(name,'') FROM sessions WHERE id=?),?)`, m.To, m.To).Scan(&name); err != nil {
		return none, false, err
	}
	verb := "was not delivered"
	switch {
	case uncertain:
		verb = "may not have arrived"
	case waiting:
		verb = "is waiting"
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
	if err := recordNudgeFire(tx, "undelivered", n.To, n.ID, n.CreatedAt); err != nil {
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
	return s.undeliveredNotice(tx, m, reason, uncertain, false)
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
	return s.undeliveredNotice(tx, m, reason, uncertain, false)
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

// NotDelivered is a report that a message reached its session's harness but no
// turn ran for it: the turn failed, or none started. The message keeps its
// state; the sender gets the notice once. It reports the notice to dispatch.
func (s *Store) NotDelivered(id, reason string) (notice protocol.Message, noticed bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return notice, false, err
	}
	defer tx.Rollback()
	m, ok, err := s.message(tx, "m.id=?", id)
	if err != nil || !ok {
		return notice, false, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "the session ran no turn for it"
	}
	if notice, noticed, err = s.undeliveredNotice(tx, m, reason, false, false); err != nil {
		return protocol.Message{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return protocol.Message{}, false, err
	}
	s.Event("msg.not_delivered", "", m.To, id, reason)
	if noticed {
		s.logMessage(notice)
	}
	return notice, noticed, nil
}

// WaitingNotice stores the early notice to the sender of a queued message that
// agent-net will not wake its recipient for: the reason says why it waits. It
// reports the notice to dispatch. It is stored once per message.
func (s *Store) WaitingNotice(m protocol.Message, reason string) (protocol.Message, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, false, err
	}
	defer tx.Rollback()
	notice, noticed, err := s.undeliveredNotice(tx, m, reason, false, true)
	if err != nil {
		return protocol.Message{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Message{}, false, err
	}
	if noticed {
		s.logMessage(notice)
	}
	return notice, noticed, nil
}
