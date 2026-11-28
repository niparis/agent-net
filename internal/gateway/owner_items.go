package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0196: the owner has no Desk. The few gateway events that genuinely
// need the owner's attention (held messages, @owner delivery steps, sign-in
// requests and permission proposals) send one plain message to the chief of
// staff (or log when there is no chief).

// askDB is what both *sql.DB and *sql.Tx offer.
type askDB interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// agentNetSender is the reserved sender of gateway-generated messages.
const agentNetSender = "agent-net"

// ownerItemKeyPrefix starts the idempotency key of a message sent to the chief.
const ownerItemKeyPrefix = "owner_item:"

// askFirstLine is the first line of s, cut to max runes.
func askFirstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(protocol.CleanWaitingText(s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

// sendToChief stores one plain message to the registered chief of staff in db,
// or logs when no chief is registered (or the sender is the chief). If ref is
// non-empty, duplicate sends with the same ref and normalized body are ignored.
// Messages stored inside a transaction are dispatched on the next tick by
// dispatchChiefNotices.
func (s *Store) sendToChief(db askDB, from, ref, body string) (protocol.Message, bool, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return protocol.Message{}, false, nil
	}
	chief, err := chiefSession(db)
	if err != nil {
		return protocol.Message{}, false, err
	}
	if chief == "" || chief == from {
		s.logf("chief.no_chief", "from", from, "ref", ref, "text", askFirstLine(body, 160))
		return protocol.Message{}, false, nil
	}
	typ := protocol.TypeRequest
	if from == "" || from == agentNetSender {
		from, typ = agentNetSender, protocol.TypeNotification
	} else {
		var known int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, from).Scan(&known); err != nil {
			return protocol.Message{}, false, err
		}
		if known == 0 {
			from, typ = agentNetSender, protocol.TypeNotification
		}
	}
	key := ownerItemKeyPrefix + protocol.NewID("k")
	if ref != "" {
		sum := sha256.Sum256([]byte(protocol.NormalizeAskText(body)))
		key = ownerItemKeyPrefix + ref + ":" + hex.EncodeToString(sum[:6])
		var sent string
		err := db.QueryRow(`SELECT id FROM messages WHERE sender=? AND idem_key=?`, from, key).Scan(&sent)
		if err == nil {
			return protocol.Message{ID: sent, From: from, To: chief, Type: typ}, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return protocol.Message{}, false, err
		}
	}
	if len(body) > protocol.MaxBodyBytes {
		body = strings.ToValidUTF8(body[:protocol.MaxBodyBytes], "")
	}
	now := s.now().UTC()
	m := protocol.Message{
		ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: from, To: chief, Type: typ, Body: body, IdempotencyKey: key,
		CreatedAt: now, ExpiresAt: now.Add(protocol.DefaultTTL), State: protocol.StateStored,
	}
	if _, err := db.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, "", m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return protocol.Message{}, false, err
	}
	s.logf("chief.notice", "from", from, "to", chief, "ref", ref, "message", m.ID)
	return m, true, nil
}

// dispatchChiefNotices dispatches stored chief notices written inside a store
// transaction.
func (g *Gateway) dispatchChiefNotices(ctx context.Context) {
	rows, err := g.store.db.Query(msgSelect+` WHERE m.state=? AND m.idem_key LIKE ?`, protocol.StateStored, ownerItemKeyPrefix+"%")
	if err != nil {
		return
	}
	var msgs []protocol.Message
	for rows.Next() {
		if m, err := scanMessage(rows); err == nil {
			msgs = append(msgs, m)
		}
	}
	rows.Close()
	for _, m := range msgs {
		g.dispatch(ctx, m)
	}
}

// ctoOpCallText is the exact CTO call a chief notice or log line names:
// cto_op with the method and its arguments.
func ctoOpCallText(method string, args map[string]any) string {
	raw, _ := json.Marshal(struct {
		Method string         `json:"method"`
		Args   map[string]any `json:"args"`
	}{method, args})
	return "cto_op " + string(raw)
}
