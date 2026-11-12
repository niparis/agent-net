package gateway

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// notStartedKeyPrefix marks agent-net's notice that a start never became a
// worker. It is delivered through the normal queue and is not a reminder.
const notStartedKeyPrefix = "notstarted:"

func isNotStartedNotice(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, notStartedKeyPrefix)
}

// maxNotStartedReason bounds the machine's words in a not-started notice.
const maxNotStartedReason = 300

// oneLine makes a machine's text one short line: control characters and line
// breaks become spaces, and agent-net's own markers are broken.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "..."
	}
	return protocol.NeutralizeMarkers(s)
}

// notStartedReasonText formats the reason for a not-started notice.
// If the reason contains registered profiles, it preserves newlines and does not truncate.
func notStartedReasonText(reason string) string {
	if strings.Contains(reason, "registered profiles:") {
		return protocol.NeutralizeMarkers(strings.TrimSpace(reason))
	}
	return oneLine(reason, maxNotStartedReason)
}

// notStartedNotice stores the notice that a tracked start will not become a
// worker, for the session that asked for it. It comes from the reserved
// agent-net sender, as the other notices of agent-net do. It never comes from
// the owner session of a machine: that sender carries the owner's label and
// authority in every harness frame, and a system notice must not.
//
// The recipient, project, harness and permissions come from the gateway's own
// record of the start. The machine only supplies the reason, which is cut to
// one short line and marked as the machine's words. No notice is stored for
// the owner (the TUI feed shows the event), for a bot, or for a session that
// is gone.
func (s *Store) notStartedNotice(tx *sql.Tx, w workerStart, host, reason string) (protocol.Message, bool, error) {
	var none protocol.Message
	var harness string
	err := tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, w.Owner).Scan(&harness)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return none, false, nil
		}
		return none, false, err
	}
	if harness == "bot" || protocol.IsOwner(harness) {
		return none, false, nil
	}
	why := "the machine " + oneLine(host, 60) + " reported: " + notStartedReasonText(reason)
	if strings.Contains(reason, "registered profiles:") {
		why = "the machine " + oneLine(host, 60) + " reported:\n" + notStartedReasonText(reason)
	}
	body := protocol.WorkerNotStartedText(oneLine(w.Project, 100), oneLine(w.Harness, 40), why) +
		protocol.WorkerPermissionsText(oneLine(w.Permissions, 40), oneLine(w.Harness, 40)) + "\n"
	now := s.now()
	n := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: w.Owner, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: notStartedKeyPrefix + w.Ref, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	res, err := tx.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, n.ID, n.ConversationID, n.ParentID, n.From, n.To, n.Type, n.Body,
		n.IdempotencyKey, n.CreatedAt.UnixNano(), n.ExpiresAt.UnixNano(), n.State)
	if err != nil {
		return none, false, err
	}
	if c, err := res.RowsAffected(); err != nil || c == 0 {
		return none, false, err
	}
	s.event(tx, "msg.stored", "", n.From, n.ID, "type=notification by=agent-net not_started")
	return n, true, nil
}

// startReportNotice stores agent-net's notice about the end of an accepted
// start, once per start id.
func (s *Store) startReportNotice(owner, id, body string) (protocol.Message, bool, error) {
	var none protocol.Message
	now := s.now()
	n := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: owner, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: notStartedKeyPrefix + "report:" + id, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, n.ID, n.ConversationID, n.ParentID, n.From, n.To, n.Type, n.Body,
		n.IdempotencyKey, n.CreatedAt.UnixNano(), n.ExpiresAt.UnixNano(), n.State)
	if err != nil {
		return none, false, err
	}
	if c, err := res.RowsAffected(); err != nil || c == 0 {
		return none, false, err
	}
	s.Event("msg.stored", "", n.From, n.ID, "type=notification by=agent-net start_report")
	return n, true, nil
}
