package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session that waits for its user (a permission prompt, a question) cannot
// act on messages. Its senders would hear nothing and ask again (decision
// 0057). So when a request is delivered to such a session, agent-net tells the
// sender once per waiting period. The request itself is delivered as usual.

// waitingKeyPrefix marks agent-net's notice that a request reached a session
// that waits for its user. The key names the recipient, the start of its wait
// and the sender, so each sender gets at most one notice per waiting period,
// also across gateway restarts.
const waitingKeyPrefix = "waiting:"

// isWaitingNotice tells whether the reserved sender's message is a waiting
// notice. It is delivered through the normal queue and is not a reminder.
func isWaitingNotice(m protocol.Message) bool {
	return m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, waitingKeyPrefix)
}

// sessionWait returns the wait of an online session that is not a worker, and
// the session's display name. It returns nil when the session does not wait.
func sessionWait(q queryer, id string) (string, *protocol.Waiting, error) {
	var name, raw string
	var online bool
	var worker sql.NullString
	err := q.QueryRow(`SELECT COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),s.id), s.waiting, s.online, s.worker
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, id).Scan(&name, &raw, &online, &worker)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil || !online || raw == "" || (worker.Valid && worker.String != "") {
		return name, nil, err
	}
	var w protocol.Waiting
	if json.Unmarshal([]byte(raw), &w) != nil {
		return name, nil, nil
	}
	return name, protocol.CleanWaiting(&w), nil
}

// waitingText says that a session waits for its user, for a peer that wrote to
// it. It never quotes the question: waiting questions are for the owner only
// (security.md, agent reads), and a bot is outside the owner's machines. The
// kind and the start of the wait are enough to know the message will wait.
// The name comes from the session's machine, so it is cleaned and cannot
// forge agent-net's own framing.
func waitingText(name string, w *protocol.Waiting, outcome string) string {
	name = protocol.NeutralizeMarkers(html.EscapeString(protocol.CleanWaitingText(name)))
	what := "answer"
	if w.Kind == protocol.WaitingPermission {
		what = "approval"
	}
	return fmt.Sprintf("%s is waiting for its user's %s (since %s). Your message %s and will be read when the user answers. No reply is needed.",
		name, what, time.Unix(w.Since, 0).UTC().Format("2006-01-02 15:04 UTC"), outcome)
}

// waitingNotice stores one notice to the sender of a request that was just
// delivered to a session that waits for its user. No notice is stored for a
// notification or a reply, for agent-net's own messages (so a notice never
// makes another), for the owner (the TUI shows the wait) or a bot (its send
// receipt says it), or when this sender heard about this wait already.
func (s *Store) waitingNotice(tx *sql.Tx, id string) (protocol.Message, bool, error) {
	var none protocol.Message
	m, ok, err := s.message(tx, "m.id=?", id)
	if err != nil || !ok || m.Type != protocol.TypeRequest || m.From == "agent-net" {
		return none, false, err
	}
	var harness string
	err = tx.QueryRow(`SELECT harness FROM sessions WHERE id=?`, m.From).Scan(&harness)
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}
	if err != nil || harness == "bot" || protocol.IsOwner(harness) {
		return none, false, err
	}
	name, w, err := sessionWait(tx, m.To)
	if err != nil || w == nil || w.Since == 0 {
		return none, false, err
	}
	now := s.now()
	n := protocol.Message{ID: protocol.NewID("m"), ConversationID: m.ConversationID, ParentID: m.ID,
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: m.From, Type: protocol.TypeNotification,
		Body:           "[agent-net] " + waitingText(name, w, "was delivered"),
		IdempotencyKey: fmt.Sprintf("%s%s:%d:%s", waitingKeyPrefix, m.To, w.Since, m.From),
		CreatedAt:      now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	res, err := tx.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, n.ID, n.ConversationID, n.ParentID, n.From, n.To, n.Type, n.Body,
		n.IdempotencyKey, n.CreatedAt.UnixNano(), n.ExpiresAt.UnixNano(), n.State)
	if err != nil {
		return none, false, err
	}
	if c, err := res.RowsAffected(); err != nil || c == 0 {
		return none, false, err
	}
	s.event(tx, "msg.stored", "", n.From, n.ID, "type=notification by=agent-net waiting="+m.ID)
	return n, true, nil
}

// waitingReceiptNote is the send receipt's note for a bot when the recipient
// waits for its user, or "".
func (s *Store) waitingReceiptNote(recipient string) string {
	name, w, err := sessionWait(s.db, recipient)
	if err != nil || w == nil || w.Since == 0 {
		return ""
	}
	return waitingText(name, w, "was sent")
}

// keepWaitSince keeps the start of a wait that a daemon reports again, for
// example after it restarted: the same kind and text is the same wait.
func keepWaitSince(stored string, w *protocol.Waiting) {
	if stored == "" || w == nil {
		return
	}
	var old protocol.Waiting
	if json.Unmarshal([]byte(stored), &old) != nil {
		return
	}
	if old.Kind == w.Kind && old.Text == w.Text && old.Since > 0 && (w.Since == 0 || old.Since < w.Since) {
		w.Since = old.Since
	}
}
