package gateway

import (
	"database/sql"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// CreateInboxItem stores an item for the owner. Callers must derive the session
// and node from authenticated state rather than trusting agent-supplied values.
func (s *Store) CreateInboxItem(item protocol.InboxItem) (protocol.InboxItem, error) {
	switch item.Kind {
	case "approval", "feature_idea", "question", "rejection", "escalation":
	default:
		return item, reject("invalid inbox kind")
	}
	if strings.TrimSpace(item.SessionID) == "" || strings.TrimSpace(item.Text) == "" {
		return item, reject("inbox session and text are required")
	}
	if len(item.Text) > protocol.MaxBodyBytes || len(item.Recommendation) > protocol.MaxBodyBytes {
		return item, reject("inbox text too long")
	}
	item.ID = protocol.NewID("i")
	item.CreatedAt = s.now().UTC()
	item.Status = "open"
	item.Answer = ""
	item.AnsweredAt = time.Time{}
	_, err := s.db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status) VALUES (?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Kind, item.SessionID, item.Node, item.Text, item.Recommendation, item.MessageID, item.ConversationID, item.CreatedAt.UnixNano(), item.Status)
	return item, err
}

type inboxScanner interface{ Scan(...any) error }

func scanInbox(row inboxScanner) (protocol.InboxItem, error) {
	var item protocol.InboxItem
	var created, answered int64
	err := row.Scan(&item.ID, &item.Kind, &item.SessionID, &item.Node, &item.Text, &item.Recommendation, &item.MessageID, &item.ConversationID, &created, &item.Status, &item.Answer, &answered)
	if err != nil {
		return item, err
	}
	item.CreatedAt = time.Unix(0, created).UTC()
	if answered != 0 {
		item.AnsweredAt = time.Unix(0, answered).UTC()
	}
	return item, nil
}

const inboxColumns = `id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at`

func (s *Store) ListInbox() ([]protocol.InboxItem, error) {
	rows, err := s.db.Query(`SELECT ` + inboxColumns + ` FROM inbox_items ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []protocol.InboxItem{}
	for rows.Next() {
		item, err := scanInbox(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AnswerInbox records an answer and queues its delivery in one transaction.
func (s *Store) AnswerInbox(id, answer string) (protocol.InboxItem, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.InboxItem{}, err
	}
	defer tx.Rollback()
	item, err := scanInbox(tx.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return item, reject("inbox item not found")
	}
	if err != nil {
		return item, err
	}
	if item.Status != "open" {
		return item, reject("inbox item already answered")
	}
	if strings.TrimSpace(answer) == "" || len(answer) > protocol.MaxBodyBytes {
		return item, reject("invalid answer")
	}
	switch answer {
	case "approve":
		item.Status = "approved"
	case "refuse":
		item.Status = "refused"
	default:
		item.Status = "answered"
	}
	item.Answer = answer
	item.AnsweredAt = s.now().UTC()
	if _, err = tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
		return item, err
	}
	var sender string
	err = tx.QueryRow(`SELECT o.id FROM sessions o JOIN sessions r ON r.node_id=o.node_id WHERE r.id=? AND o.harness=? LIMIT 1`, item.SessionID, protocol.OwnerHarness).Scan(&sender)
	if err == sql.ErrNoRows {
		return item, reject("recipient or owner session not found")
	}
	if err != nil {
		return item, err
	}
	note := ""
	if item.Kind == "approval" && answer == "approve" {
		note = "Nicolas approved: " + protocol.NeutralizeMarkers(item.Text) + ", " + item.AnsweredAt.Format(time.RFC3339)
	}
	_, err = tx.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state,approval_note) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		protocol.NewID("m"), protocol.NewID("c"), "", sender, item.SessionID, protocol.TypeNotification, answer, 0, protocol.NewID("k"), item.AnsweredAt.UnixNano(), item.AnsweredAt.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored, note)
	if err != nil {
		return item, err
	}
	if err = tx.Commit(); err != nil {
		return item, err
	}
	return item, nil
}
