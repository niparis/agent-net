package daemon

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude session answers a peer with its own SendMessage tool, which names
// a reply address but not the message it answers. The Claude adapter keeps
// every message it delivers in daemon.db, so it can match the answer to the
// request after a daemon restart too, and so two open requests do not cross
// (W4.4, decision 0053).

// ClaudeDelivery is one message delivered to a Claude session.
type ClaudeDelivery struct {
	MessageID      string
	Recipient      string // the Claude session
	Sender         string
	ConversationID string
	Type           string // only a request can be open
	DeliveredAt    time.Time
	// ExpiresAt is the message expiry. Zero means DeliveredAt plus the
	// message expiry.
	ExpiresAt time.Time
	// Frame is the message ID of the one frame written to the inbox. It is
	// the message itself, or the carrier of a batch. Frame is empty on a row
	// saved before the frame was built.
	Frame string
	// ReplySock is the reply socket the frame names.
	ReplySock string
	// Preview is the first line of the body, as Claude Code shows it in a
	// hold notice.
	Preview string
	// BodyLines and BodyChars describe the body in the hold notice's own
	// counts. Zero when the body is empty.
	BodyLines int
	BodyChars int
	// FrameHash is the hash of the exact frame content, which is what the
	// session writes back when it releases the frame.
	FrameHash string
}

// ReplyAddress is the sender and conversation of one reply socket.
type ReplyAddress struct {
	Sender         string
	ConversationID string
}

// How a request was closed, kept in the answer column.
const (
	// ReplyMatchExact: the reply was sent as the answer to this request.
	ReplyMatchExact = "reply"
	// ReplyMatchConversation: several requests were open and the reply did
	// not say which one it answers, so it went to the conversation with no
	// parent. Every open request there is closed by it.
	ReplyMatchConversation = "conversation"
)

// ReplyBook is what the Claude adapter asks of daemon.db.
type ReplyBook interface {
	// SaveDelivery records a message before it is written to the session.
	// Saving it again keeps the first row and its answer.
	SaveDelivery(ClaudeDelivery) error
	// SaveFrame records how the one frame written to the inbox appears: its
	// head message, reply socket and the body a hold notice quotes. For a
	// batch, the head row is the carrier, typed "batch" so it is not matched
	// as an open request.
	SaveFrame(ClaudeDelivery) error
	// ForgetDelivery removes the row of a message that never reached the
	// session.
	ForgetDelivery(messageID string) error
	// OpenRequests returns the unanswered, unexpired requests from sender
	// to recipient, newest first. An empty conversation matches any.
	OpenRequests(recipient, sender, conversation string, now time.Time) ([]ClaudeDelivery, error)
	// CloseRequests marks requests answered, with how the answer matched.
	CloseRequests(ids []string, match string, at time.Time) error
	// ReplyAddresses lists each sender and conversation that has a kept row.
	ReplyAddresses() ([]ReplyAddress, error)
}

// claudeReplyMargin is how long a row outlives its message. It keeps the
// reply address of a conversation open for a late answer after the request
// expired; such an answer goes to the conversation.
const claudeReplyMargin = 24 * time.Hour

const claudeDeliveriesSchema = `
CREATE TABLE IF NOT EXISTS claude_deliveries (
	message_id TEXT PRIMARY KEY, recipient TEXT NOT NULL, sender TEXT NOT NULL,
	conversation_id TEXT NOT NULL, type TEXT NOT NULL,
	delivered_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
	answered_at INTEGER NOT NULL DEFAULT 0, answer TEXT NOT NULL DEFAULT '',
	frame TEXT NOT NULL DEFAULT '', reply_sock TEXT NOT NULL DEFAULT '',
	preview TEXT NOT NULL DEFAULT '', body_lines INTEGER NOT NULL DEFAULT 0,
	body_chars INTEGER NOT NULL DEFAULT 0, frame_hash TEXT NOT NULL DEFAULT '',
	held_at INTEGER NOT NULL DEFAULT 0, released_at INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS claude_deliveries_open ON claude_deliveries(recipient, sender, conversation_id, delivered_at);
-- One row per hold notice from a transcript, so a restart does not apply an
-- old notice to another delivery.
CREATE TABLE IF NOT EXISTS claude_hold_entries (
	entry TEXT PRIMARY KEY, recipient TEXT NOT NULL, message_id TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL);`

func (s *state) SaveDelivery(r ClaudeDelivery) error {
	if r.DeliveredAt.IsZero() {
		r.DeliveredAt = time.Now()
	}
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = r.DeliveredAt.Add(protocol.DefaultTTL)
	}
	_, err := s.db.Exec(`INSERT INTO claude_deliveries (message_id, recipient, sender, conversation_id, type, delivered_at, expires_at)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT (message_id) DO NOTHING`,
		r.MessageID, r.Recipient, r.Sender, r.ConversationID, r.Type, r.DeliveredAt.UnixNano(), r.ExpiresAt.UnixNano())
	return err
}

func (s *state) ForgetDelivery(id string) error {
	_, err := s.db.Exec(`DELETE FROM claude_deliveries WHERE message_id = ?`, id)
	return err
}

func (s *state) OpenRequests(recipient, sender, conversation string, now time.Time) ([]ClaudeDelivery, error) {
	q := `SELECT message_id, recipient, sender, conversation_id, type, delivered_at, expires_at FROM claude_deliveries
		WHERE recipient = ? AND sender = ? AND type = ? AND answered_at = 0 AND expires_at > ?`
	args := []any{recipient, sender, protocol.TypeRequest, now.UnixNano()}
	if conversation != "" {
		q += ` AND conversation_id = ?`
		args = append(args, conversation)
	}
	rows, err := s.db.Query(q+` ORDER BY delivered_at DESC, rowid DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaudeDelivery
	for rows.Next() {
		var r ClaudeDelivery
		var at, exp int64
		if err := rows.Scan(&r.MessageID, &r.Recipient, &r.Sender, &r.ConversationID, &r.Type, &at, &exp); err != nil {
			return nil, err
		}
		r.DeliveredAt, r.ExpiresAt = time.Unix(0, at), time.Unix(0, exp)
		out = append(out, r)
	}
	return out, rows.Err()
}

// heldFresh is how long before a hold notice its frame can have been written.
// A hold follows the inbox write at once; this only keeps an old notice from
// matching a much older message with the same text.
const heldFresh = 15 * time.Minute

// heldClockSlack tolerates a transcript clock a moment behind the daemon.
const heldClockSlack = 5 * time.Second

// SaveFrame records how the one frame written to the inbox appears to Claude
// Code: its head message, reply socket and the body a hold notice quotes. For
// a batch the head is the carrier; its row is typed "batch" so reply matching
// never treats it as an open request.
func (s *state) SaveFrame(f ClaudeDelivery) error {
	if f.DeliveredAt.IsZero() {
		f.DeliveredAt = time.Now()
	}
	if f.ExpiresAt.IsZero() {
		f.ExpiresAt = f.DeliveredAt.Add(protocol.DefaultTTL)
	}
	_, err := s.db.Exec(`INSERT INTO claude_deliveries
		(message_id, recipient, sender, conversation_id, type, delivered_at, expires_at, frame, reply_sock, preview, body_lines, body_chars, frame_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (message_id) DO UPDATE SET
			frame=excluded.frame, reply_sock=excluded.reply_sock, preview=excluded.preview,
			body_lines=excluded.body_lines, body_chars=excluded.body_chars, frame_hash=excluded.frame_hash`,
		f.MessageID, f.Recipient, f.Sender, f.ConversationID, f.Type, f.DeliveredAt.UnixNano(), f.ExpiresAt.UnixNano(),
		f.Frame, f.ReplySock, f.Preview, f.BodyLines, f.BodyChars, f.FrameHash)
	return err
}

// markClaudeHeld records that Claude Code kept one frame for review. It picks
// the one unheld frame from the notice's reply socket whose first body line is
// the notice's preview and that was written just before the notice. When that
// is not one frame, it records the notice and marks nothing: a hold is never
// guessed.
//
// The notice's own id keys claude_hold_entries, so a daemon restart that reads
// the transcript again applies each notice once. handled reports that case.
func (s *state) markClaudeHeld(recipient string, e ClaudeHeldEntry) (r ClaudeDelivery, ok, handled bool, err error) {
	if e.At.IsZero() {
		return r, false, false, nil
	}
	key := e.UUID
	if key == "" {
		// An older notice without its own id: key it by what it says.
		sum := sha256.Sum256([]byte(e.ReplySock + "\n" + e.Preview + "\n" + e.At.UTC().Format(time.RFC3339Nano)))
		key = fmt.Sprintf("text:%x", sum)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return r, false, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO claude_hold_entries (entry, recipient, at) VALUES (?,?,?) ON CONFLICT (entry) DO NOTHING`,
		key, recipient, e.At.UnixNano())
	if err != nil {
		return r, false, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return r, false, false, err
	}
	if n == 0 {
		return r, false, true, nil
	}
	rows, err := tx.Query(`SELECT message_id, recipient, sender, conversation_id, type, delivered_at, expires_at,
			frame, reply_sock, preview, body_lines, body_chars, frame_hash
		FROM claude_deliveries
		WHERE recipient=? AND message_id=frame AND reply_sock=? AND held_at=0 AND released_at=0
			AND answered_at=0 AND expires_at>? AND delivered_at BETWEEN ? AND ?
		ORDER BY delivered_at DESC, rowid DESC`,
		recipient, e.ReplySock, time.Now().UnixNano(),
		e.At.Add(-heldFresh).UnixNano(), e.At.Add(heldClockSlack).UnixNano())
	if err != nil {
		return r, false, false, err
	}
	var match []ClaudeDelivery
	for rows.Next() {
		var c ClaudeDelivery
		var delivered, expires int64
		if err := rows.Scan(&c.MessageID, &c.Recipient, &c.Sender, &c.ConversationID, &c.Type, &delivered, &expires,
			&c.Frame, &c.ReplySock, &c.Preview, &c.BodyLines, &c.BodyChars, &c.FrameHash); err != nil {
			rows.Close()
			return r, false, false, err
		}
		c.DeliveredAt, c.ExpiresAt = time.Unix(0, delivered), time.Unix(0, expires)
		if previewMatches(e.Preview, c.Preview) {
			match = append(match, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, false, false, err
	}
	if len(match) > 1 && (e.BodyLines > 0 || e.BodyChars > 0) {
		// The notice's own line and character counts tell two frames with the
		// same first line apart.
		var exact []ClaudeDelivery
		for _, c := range match {
			if c.BodyLines == e.BodyLines && c.BodyChars == e.BodyChars {
				exact = append(exact, c)
			}
		}
		if len(exact) > 0 {
			match = exact
		}
	}
	if len(match) != 1 {
		// Keep the notice, so a restart logs it once and does not try again.
		if err := tx.Commit(); err != nil {
			return r, false, false, err
		}
		return r, false, false, nil
	}
	r = match[0]
	if _, err := tx.Exec(`UPDATE claude_deliveries SET held_at=?, held_entry=? WHERE message_id=? AND held_at=0`,
		e.At.UnixNano(), key, r.MessageID); err != nil {
		return r, false, false, err
	}
	if _, err := tx.Exec(`UPDATE claude_hold_entries SET message_id=? WHERE entry=?`, r.MessageID, key); err != nil {
		return r, false, false, err
	}
	if err := tx.Commit(); err != nil {
		return r, false, false, err
	}
	return r, true, false, nil
}

// previewMatches compares the first body line the notice quotes with the one
// the frame was built with. Claude Code may cut a long line with an ellipsis.
func previewMatches(notice, stored string) bool {
	notice = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(notice), "\u2026"))
	stored = strings.TrimSpace(stored)
	if notice == "" || stored == "" {
		return false
	}
	if notice == stored {
		return true
	}
	short, long := notice, stored
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= 20 && strings.HasPrefix(long, short)
}

// releaseClaudeHeld marks the held frames that the release writes back. The
// frame's own body hash and reply socket tie it to one delivery, never a
// message ID in the body or in agent-net's cited notes. It returns the head
// message IDs that were still held.
func (s *state) releaseClaudeHeld(recipient, replySock, frameHash string, at time.Time) ([]string, error) {
	if at.IsZero() {
		at = time.Now()
	}
	if frameHash == "" {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT message_id FROM claude_deliveries
		WHERE recipient=? AND reply_sock=? AND frame_hash=? AND held_at>0 AND released_at=0`,
		recipient, replySock, frameHash)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE claude_deliveries SET released_at=? WHERE message_id=? AND released_at=0`, at.UnixNano(), id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *state) CloseRequests(ids []string, match string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{at.UnixNano(), match}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.Exec(`UPDATE claude_deliveries SET answered_at = ?, answer = ? WHERE answered_at = 0 AND message_id IN (?`+
		strings.Repeat(",?", len(ids)-1)+`)`, args...)
	return err
}

func (s *state) ReplyAddresses() ([]ReplyAddress, error) {
	rows, err := s.db.Query(`SELECT DISTINCT sender, conversation_id FROM claude_deliveries WHERE conversation_id <> '' ORDER BY sender, conversation_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplyAddress
	for rows.Next() {
		var a ReplyAddress
		if err := rows.Scan(&a.Sender, &a.ConversationID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// pruneClaudeDeliveries deletes rows whose message expired more than
// claudeReplyMargin ago. It returns how many rows went.
func (s *state) pruneClaudeDeliveries(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM claude_deliveries WHERE expires_at < ?`, now.Add(-claudeReplyMargin).UnixNano())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
