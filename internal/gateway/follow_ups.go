package gateway

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A follow-up is a check the sender of a message, or the chief who sorts an
// ask, sets for a future event (follow_up_at). The chief has no timer, so the
// minute tick looks at every due follow-up. If the expected event has not
// happened, it raises one ask routed to the chief. No model tokens are used.
const followUpsMigration = `
CREATE TABLE IF NOT EXISTS follow_ups (
 id TEXT PRIMARY KEY,
 event TEXT NOT NULL CHECK (event IN ('reply','change','conversation','session')),
 set_by TEXT NOT NULL, waiter TEXT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '', ask_id TEXT NOT NULL DEFAULT '',
 conversation_id TEXT NOT NULL DEFAULT '', recipient TEXT NOT NULL DEFAULT '',
 change_id TEXT NOT NULL DEFAULT '', watch_session TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, due_at INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','met','raised')));
CREATE INDEX IF NOT EXISTS follow_ups_due ON follow_ups(status, due_at);
`

type followUp struct {
	id, event, setBy, waiter        string
	messageID, askID                string
	conversationID, recipient       string
	changeID, watchSession, summary string
	createdAt, dueAt                int64
}

func (f followUp) insert(q askDB) error {
	_, err := q.Exec(`INSERT INTO follow_ups(id,event,set_by,waiter,message_id,ask_id,conversation_id,recipient,change_id,watch_session,summary,created_at,due_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.id, f.event, f.setBy, f.waiter, f.messageID, f.askID, f.conversationID, f.recipient, f.changeID, f.watchSession, f.summary, f.createdAt, f.dueAt)
	return err
}

// recordSendFollowUp stores the follow-up of a message: the expected event is a
// reply in its thread from the recipient.
func recordSendFollowUp(q askDB, m protocol.Message, raw string, now time.Time) error {
	due, err := protocol.ParseFollowUp(raw, now)
	if err != nil {
		return reject("%v", err)
	}
	return followUp{id: protocol.NewID("f"), event: "reply", setBy: m.From, waiter: m.From, messageID: m.ID,
		conversationID: m.ConversationID, recipient: m.To, summary: askFirstLine(m.Body, 120),
		createdAt: now.UnixNano(), dueAt: due.UnixNano()}.insert(q)
}

// recordSortFollowUp stores the follow-up of a send or close sort. The expected
// event is, in order: a move of the change the sort names (or the ask is about),
// any later message in the ask's conversation, and last any later message to or
// from the asker.
func (s *Store) recordSortFollowUp(tx *sql.Tx, q protocol.AskSortRequest, a askSortRow, sorter string, now time.Time) error {
	due, err := protocol.ParseFollowUp(q.FollowUpAt, now)
	if err != nil {
		return reject("%v", err)
	}
	var messageID, conversation string
	if err := tx.QueryRow(`SELECT message_id, conversation_id FROM inbox_items WHERE id=?`, q.Ask).Scan(&messageID, &conversation); err != nil {
		return err
	}
	f := followUp{id: protocol.NewID("f"), setBy: sorter, waiter: a.session, askID: q.Ask, messageID: messageID,
		summary: askFirstLine(a.text, 120), createdAt: now.UnixNano(), dueAt: due.UnixNano()}
	change := q.Change
	if change == "" && a.aboutKind == protocol.AskAboutChange {
		change = a.aboutID
	} else if change == "" && a.aboutKind == protocol.AskAboutStep {
		change, _, _, _, _ = parseStepAboutID(a.aboutID)
	}
	switch {
	case change != "":
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM delivery_changes WHERE id=?`, change).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return reject("unknown change %q", change)
		}
		f.event, f.changeID = "change", change
	default:
		// A stall ask keeps a number in conversation_id, so only a real
		// conversation counts.
		var n int
		if conversation != "" {
			if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, conversation).Scan(&n); err != nil {
				return err
			}
		}
		if n > 0 {
			f.event, f.conversationID = "conversation", conversation
		} else {
			f.event, f.watchSession = "session", a.session
		}
	}
	return f.insert(tx)
}

// eventHappened says whether the event a follow-up waits for has happened since
// it was set.
func eventHappened(q askDB, f followUp) (bool, error) {
	var n int
	var err error
	switch f.event {
	case "reply":
		err = q.QueryRow(`SELECT
			EXISTS(SELECT 1 FROM messages WHERE id=? AND state IN (?,?))
			OR EXISTS(SELECT 1 FROM messages WHERE conversation_id=? AND sender=? AND created_at>?)`,
			f.messageID, protocol.StateReplied, protocol.StateAcknowledged, f.conversationID, f.recipient, f.createdAt).Scan(&n)
	case "change":
		// Evidence and change rows count whole seconds.
		sec := f.createdAt / int64(time.Second)
		err = q.QueryRow(`SELECT
			EXISTS(SELECT 1 FROM delivery_evidence WHERE change_id=? AND at>?)
			OR EXISTS(SELECT 1 FROM delivery_changes WHERE id=? AND updated_at>?)`, f.changeID, sec, f.changeID, sec).Scan(&n)
	case "conversation":
		err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE conversation_id=? AND sender<>'agent-net' AND created_at>?)`,
			f.conversationID, f.createdAt).Scan(&n)
	default:
		err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE (sender=? OR recipient=?) AND sender<>'agent-net' AND created_at>?)`,
			f.watchSession, f.watchSession, f.createdAt).Scan(&n)
	}
	return n > 0, err
}

// expectedText says what the follow-up waited for.
func (f followUp) expectedText(q askDB) string {
	switch f.event {
	case "reply":
		return fmt.Sprintf("a reply from %s (%s) to message %s (%s)", sessionLabel(q, f.recipient), f.recipient, f.messageID, f.summary)
	case "change":
		return fmt.Sprintf("a step of change %s to move (new evidence or a new revision)", f.changeID)
	case "conversation":
		return fmt.Sprintf("a later message in conversation %s (ask %s: %s)", f.conversationID, f.askID, f.summary)
	}
	return fmt.Sprintf("a later message from or to %s (%s) (ask %s: %s)", sessionLabel(q, f.watchSession), f.watchSession, f.askID, f.summary)
}

// checkFollowUps raises one ask to the chief for every due follow-up whose
// event has not happened. A follow-up is looked at once, when it falls due.
func (s *Store) checkFollowUps() error {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,event,set_by,waiter,message_id,ask_id,conversation_id,recipient,change_id,watch_session,summary,created_at,due_at
		FROM follow_ups WHERE status='pending' AND due_at<=? ORDER BY due_at,id`, now.UnixNano())
	if err != nil {
		return err
	}
	var due []followUp
	for rows.Next() {
		var f followUp
		if err := rows.Scan(&f.id, &f.event, &f.setBy, &f.waiter, &f.messageID, &f.askID, &f.conversationID, &f.recipient, &f.changeID,
			&f.watchSession, &f.summary, &f.createdAt, &f.dueAt); err != nil {
			rows.Close()
			return err
		}
		due = append(due, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range due {
		met, err := eventHappened(tx, f)
		if err != nil {
			return err
		}
		status := "met"
		if !met {
			status = "raised"
			text := fmt.Sprintf("%s (%s) set a follow-up and the expected event has not happened. Expected: %s. Set %s, due %s.",
				sessionLabel(tx, f.setBy), f.setBy, f.expectedText(tx),
				time.Unix(0, f.createdAt).UTC().Format(time.RFC3339), time.Unix(0, f.dueAt).UTC().Format(time.RFC3339))
			var node string
			_ = tx.QueryRow(`SELECT COALESCE(repo_project,'') FROM sessions WHERE id=?`, f.waiter).Scan(&node)
			item := protocol.InboxItem{
				Kind: protocol.AskKindQuestion, FromKind: protocol.AskFromGateway, SessionID: f.waiter, Node: node, NextWho: protocol.AskNextChief,
				SourceRef: "follow-up:" + f.id, MessageID: f.messageID, ConversationID: f.conversationID,
				Text: text, Options: []string{"wait", "close"},
			}
			if _, err := s.createInboxItem(tx, item); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE follow_ups SET status=? WHERE id=?`, status, f.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
