package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// notStartedKeyPrefix marks agent-net's notice that a start never became a
// worker. It is delivered through the normal queue and is not a reminder.
const notStartedKeyPrefix = "notstarted:"

func isNotStartedNotice(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, notStartedKeyPrefix)
}

// maxNotStartedReason bounds the reason text in a not-started notice.
const maxNotStartedReason = 1000

// notStartedNotice stores the notice that a start will not become a worker, for
// the session that asked for it. It comes from the reserved agent-net sender,
// as the other notices of agent-net do. It never comes from the owner session
// of a machine: that sender carries the owner's label and authority in every
// harness frame, and a system notice must not. The text is built here from the
// fields, and the node's text is neutralized, so a node cannot forge agent-net's
// own framing. No notice is stored for the owner (the TUI feed shows the event),
// for a bot, or for a session that is not known.
func (s *Store) notStartedNotice(p protocol.WorkerNotStarted) (protocol.Message, bool, error) {
	var none protocol.Message
	var harness string
	err := s.db.QueryRow(`SELECT harness FROM sessions WHERE id=?`, p.Owner).Scan(&harness)
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}
	if err != nil || harness == "bot" || protocol.IsOwner(harness) {
		return none, false, err
	}
	reason := p.Reason
	if len(reason) > maxNotStartedReason {
		reason = reason[:maxNotStartedReason]
	}
	body := protocol.WorkerNotStartedText(protocol.NeutralizeMarkers(p.Project), protocol.NeutralizeMarkers(p.Harness), protocol.NeutralizeMarkers(reason)) +
		protocol.WorkerPermissionsText(protocol.NeutralizeMarkers(p.Permissions), protocol.NeutralizeMarkers(p.Harness)) + "\n"
	if len(body) > protocol.MaxBodyBytes {
		body = body[:protocol.MaxBodyBytes]
	}
	now := s.now()
	n := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: p.Owner, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: notStartedKeyPrefix + protocol.NewID("k"), CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	tx, err := s.db.Begin()
	if err != nil {
		return none, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, n.ID, n.ConversationID, n.ParentID, n.From, n.To, n.Type, n.Body,
		n.IdempotencyKey, n.CreatedAt.UnixNano(), n.ExpiresAt.UnixNano(), n.State); err != nil {
		return none, false, fmt.Errorf("not-started notice: %w", err)
	}
	s.event(tx, "msg.stored", "", n.From, n.ID, "type=notification by=agent-net not_started")
	if err := tx.Commit(); err != nil {
		return none, false, err
	}
	return n, true, nil
}
