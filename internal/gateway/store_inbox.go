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
	return s.createInboxItem(s.db, item)
}

// RequestWrittenApproval adds the exact action to the owner's inbox.
func (s *Store) RequestWrittenApproval(session, node string, req protocol.ApprovalRequest) (protocol.InboxItem, error) {
	return s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: session, Node: node, Text: req.Action, Recommendation: req.Recommendation})
}

type inboxExecer interface {
	Exec(string, ...any) (sql.Result, error)
}

func (s *Store) createInboxItem(db inboxExecer, item protocol.InboxItem) (protocol.InboxItem, error) {
	switch item.Kind {
	case "approval", "feature_idea", "question", "rejection", "escalation", "check":
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
	_, err := db.Exec(`INSERT INTO inbox_items (id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status) VALUES (?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Kind, item.SessionID, item.Node, item.Text, item.Recommendation, item.MessageID, item.ConversationID, item.CreatedAt.UnixNano(), item.Status)
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
	items := []protocol.InboxItem{}
	for rows.Next() {
		item, err := scanInbox(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if item.Kind == "check" && item.Node == "apps" && strings.HasPrefix(item.Text, "Connected: ") && !item.CreatedAt.After(s.now().Add(-24*time.Hour)) {
			continue
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	byID := map[string]int{}
	for i := range items {
		byID[items[i].ID] = i
	}
	rows, err = s.db.Query(`SELECT d.item_id,d.change_id,d.step,d.for_step,d.after_merge,d.commit_hash,c.project
		FROM delivery_step_inbox d JOIN delivery_changes c ON c.id=d.change_id`)
	if err != nil {
		return nil, err
	}
	type deliveryItem struct {
		id, project string
		delivery    protocol.InboxDelivery
	}
	var deliveries []deliveryItem
	for rows.Next() {
		var d deliveryItem
		var after int
		if err := rows.Scan(&d.id, &d.delivery.ChangeID, &d.delivery.Step, &d.delivery.For, &after, &d.delivery.Commit, &d.project); err != nil {
			rows.Close()
			return nil, err
		}
		d.delivery.AfterMerge = after != 0
		deliveries = append(deliveries, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	pipelines := map[string]protocol.Pipeline{}
	for _, d := range deliveries {
		i, ok := byID[d.id]
		if !ok {
			continue
		}
		p, ok := pipelines[d.project]
		if !ok {
			p, err = s.deliveryPipeline(d.project)
			if err != nil {
				return nil, err
			}
			pipelines[d.project] = p
		}
		d.delivery.ReportFields = p.ReportFields(d.delivery.StepRef)
		items[i].Delivery = &d.delivery
	}
	return items, nil
}

// AnswerInbox records an answer and queues its delivery in one transaction.
func (s *Store) AnswerInbox(id, answer string, validate ...bool) (protocol.InboxItem, error) {
	if item, handled, err := s.answerDeliveryApproval(id, answer, validate...); handled || err != nil {
		return item, err
	}
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
	// Dismissing closes the item without answering it. It needs no recipient:
	// an old approval can outlive the session that asked for it, and the owner
	// must still be able to clear it from the Inbox. "seen" is the older
	// notice answer and means the same thing.
	if answer == "dismiss" || answer == "seen" {
		item.Status = "dismissed"
		item.Answer = ""
		item.AnsweredAt = s.now().UTC()
		if _, err = tx.Exec(`UPDATE inbox_items SET status=?,answer=?,answered_at=? WHERE id=?`, item.Status, item.Answer, item.AnsweredAt.UnixNano(), id); err != nil {
			return item, err
		}
		if len(validate) > 0 && validate[0] {
			return item, nil
		}
		if err = tx.Commit(); err != nil {
			return item, err
		}
		return item, nil
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
		note = protocol.WrittenApprovalNote("the owner", protocol.NeutralizeMarkers(item.Text), item.AnsweredAt)
	}
	_, err = tx.Exec(`INSERT INTO messages (id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state,approval_note) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		protocol.NewID("m"), protocol.NewID("c"), "", sender, item.SessionID, protocol.TypeNotification, answer, 0, protocol.NewID("k"), item.AnsweredAt.UnixNano(), item.AnsweredAt.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored, note)
	if err != nil {
		return item, err
	}
	if item.Kind == "rejection" || item.Kind == "escalation" {
		if _, err = tx.Exec(`UPDATE graph_outcomes SET resolution='answered' WHERE reply_id=? AND resolution=''`, item.MessageID); err != nil {
			return item, err
		}
	}
	if len(validate) > 0 && validate[0] {
		return item, nil
	}
	if err = tx.Commit(); err != nil {
		return item, err
	}
	return item, nil
}
