package gateway

import (
	"database/sql"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A redirected or handed-off request can leave its sender with no answer. The
// sender hears once, after the ordinary reminder grace, through the same nudge
// path as the other automatic notices (decision 0134). An owner-approved
// request also reaches the chief of staff, who curates what waits on the owner.

// redirectReminderPrefix marks agent-net's notice that a routed request has no
// reply. The ordinary notice and the chief's copy have distinct keys.
const redirectReminderPrefix = "redirect-reminder"

// redirectReminderKind is the nudge kind, so the chief can pause it.
const redirectReminderKind = "redirect_reminder"

// needsRedirectReminderCheck tells whether a queued message is a redirect
// notice, which is only delivered while its request is still open.
func needsRedirectReminderCheck(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, redirectReminderPrefix)
}

// routedRequest is a request lead routing redirected or a lead handed off.
const routedRequest = ` FROM messages q JOIN sessions src ON src.id=q.sender JOIN sessions dst ON dst.id=q.recipient
 LEFT JOIN profiles sp ON sp.session_id=src.id LEFT JOIN profiles dp ON dp.session_id=dst.id
 WHERE q.type='request' AND q.state IN ('stored','dispatched','delivered')
  AND q.sender<>'agent-net' AND q.expires_at>?
  AND (q.body LIKE 'Redirected to %' OR q.handed_off_by<>'')
  AND NOT EXISTS (SELECT 1 FROM messages r WHERE r.conversation_id=q.conversation_id
   AND r.type IN ` + answeredTypesSQL + ` AND r.created_at>=q.created_at)`

// redirectReminderEligible rechecks one notice at dispatch: the request may
// have been answered after the notice was built.
func (s *Store) redirectReminderEligible(id string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*)`+routedRequest+` AND q.id=?`, s.now().UnixNano(), id).Scan(&n)
	return n == 1, err
}

// redirectReminders stores the notices a pass makes. It runs in the reminder
// transaction, after the reply reminders, and uses that reminder's grace.
func (s *Store) redirectReminders(tx *sql.Tx) ([]protocol.Message, error) {
	now := s.now()
	rows, err := tx.Query(`SELECT q.id,q.conversation_id,q.sender,q.recipient,q.created_at,q.owner_approved,
	 COALESCE(NULLIF(sp.name,''),NULLIF(src.name,''),q.sender),
	 COALESCE(NULLIF(dp.name,''),NULLIF(dst.name,''),q.recipient),
	 src.harness,q.handed_off_by<>''`+routedRequest+` AND q.created_at>=? AND q.created_at<?`, now.UnixNano(),
		now.Add(-reminderWindow).UnixNano(), now.Add(-reminderGrace).UnixNano())
	if err != nil {
		return nil, err
	}
	type routed struct {
		id, conv, sender, recipient, senderName, recipientName, harness string
		created                                                         int64
		ownerApproved, handed                                           bool
	}
	var candidates []routed
	for rows.Next() {
		var r routed
		if err := rows.Scan(&r.id, &r.conv, &r.sender, &r.recipient, &r.created, &r.ownerApproved, &r.senderName, &r.recipientName, &r.harness, &r.handed); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []protocol.Message
	for _, r := range candidates {
		if r.harness == protocol.OwnerHarness || r.harness == "bot" || r.harness == "cto" {
			continue
		}
		age := formatAge(now.Sub(time.Unix(0, r.created)))
		to := "redirected to"
		if r.handed {
			to = "handed off to"
		}
		body := fmt.Sprintf("[agent-net] Your request %s was %s %s and has no reply after %s.\nIf it still matters, ask again or ask your lead. This is a notice from agent-net. No reply is needed.",
			r.id, to, protocol.NeutralizeMarkers(html.EscapeString(protocol.CleanWaitingText(r.recipientName))), age)
		m, stored, err := s.storeRedirectReminder(tx, r.conv, r.id, r.sender, body, redirectReminderPrefix+":"+r.id)
		if err != nil {
			return nil, err
		}
		if stored {
			out = append(out, m)
		}
		if !r.ownerApproved {
			continue
		}
		chief, err := chiefSession(tx)
		if err != nil {
			return nil, err
		}
		if chief == "" || chief == r.sender {
			continue
		}
		body = fmt.Sprintf("[agent-net] The owner approved request %s from %s, but it has no reply after %s. It was %s %s. Check whether the asker is stuck.",
			r.id, protocol.NeutralizeMarkers(html.EscapeString(protocol.CleanWaitingText(r.senderName))), age,
			to, protocol.NeutralizeMarkers(html.EscapeString(protocol.CleanWaitingText(r.recipientName))))
		m, stored, err = s.storeRedirectReminder(tx, r.conv, r.id, chief, body, redirectReminderPrefix+"-chief:"+r.id)
		if err != nil {
			return nil, err
		}
		if stored {
			out = append(out, m)
		}
	}
	return out, nil
}

// storeRedirectReminder writes one notice unless its request changed or the
// recipient's nudge is paused.
func (s *Store) storeRedirectReminder(tx *sql.Tx, conv, request, to, body, key string) (protocol.Message, bool, error) {
	var none protocol.Message
	if paused, err := s.nudgePaused(tx, redirectReminderKind, to); err != nil {
		return none, false, err
	} else if paused {
		return none, false, nil
	}
	now := s.now()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: conv, ParentID: request,
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: to, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: key, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	res, err := tx.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State)
	if err != nil {
		return none, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return none, false, err
	}
	if err := recordNudgeFire(tx, redirectReminderKind, m.To, m.ID, m.CreatedAt); err != nil {
		return none, false, err
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net redirect="+request)
	return m, true, nil
}
