package daemon

import (
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
	answered_at INTEGER NOT NULL DEFAULT 0, answer TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS claude_deliveries_open ON claude_deliveries(recipient, sender, conversation_id, delivered_at);`

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
