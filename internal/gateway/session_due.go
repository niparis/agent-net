package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Due items (agent_due) are steps a session owns. A session records one for a
// time or after an event. The minute tick wakes the session through the normal
// message path when the item falls due. If the session then shows no activity
// for dueIdleAfter, one ask goes to the chief. It is built like the follow-ups
// (follow_ups.go): the tick does the checks and no model tokens are used.
const sessionDuesMigration = `
CREATE TABLE IF NOT EXISTS session_dues (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL,
 what TEXT NOT NULL,
 due_at INTEGER NOT NULL DEFAULT 0,
 after_kind TEXT NOT NULL DEFAULT '' CHECK (after_kind IN ('','reply','change_step','ask_closed')),
 after_ref TEXT NOT NULL DEFAULT '',
 blocker TEXT NOT NULL DEFAULT 'none',
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','woken','active','raised','done')),
 woken_at INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS session_dues_open ON session_dues(status, due_at);
CREATE INDEX IF NOT EXISTS session_dues_session ON session_dues(session_id, status);
`

// dueIdleAfter is how long a woken session has to show activity before the
// chief is asked.
const dueIdleAfter = 15 * time.Minute

// Item statuses. pending waits for its time or event. woken was sent a wake
// and is checked once more. active showed activity after its wake, so no ask
// is raised. raised has its one chief ask. done was closed by its session.
const (
	dueStatusPending = "pending"
	dueStatusWoken   = "woken"
	dueStatusActive  = "active"
	dueStatusRaised  = "raised"
	dueStatusDone    = "done"
)

type sessionDue struct {
	id, session, what string
	dueAt             int64
	afterKind         string
	afterRef          string
	blocker, status   string
	wokenAt           int64
	createdAt         int64
}

// expectedText says what an item waits for.
func (d sessionDue) expectedText() string {
	switch d.afterKind {
	case "reply":
		return "a reply to message " + d.afterRef
	case "change_step":
		return "a step of change " + d.afterRef + " to move"
	case "ask_closed":
		return "ask " + d.afterRef + " to close"
	}
	return time.Unix(0, d.dueAt).UTC().Format(time.RFC3339)
}

const sessionDueColumns = `id,session_id,what,due_at,after_kind,after_ref,blocker,status,woken_at,created_at`

func scanSessionDue(sc interface{ Scan(...any) error }) (sessionDue, error) {
	var d sessionDue
	err := sc.Scan(&d.id, &d.session, &d.what, &d.dueAt, &d.afterKind, &d.afterRef, &d.blocker, &d.status, &d.wokenAt, &d.createdAt)
	return d, err
}

// dueItemOf is the answer form of an item.
func dueItemOf(d sessionDue) protocol.DueItem {
	item := protocol.DueItem{ID: d.id, What: d.what, Status: d.status, Blocker: d.blocker,
		CreatedAt: time.Unix(0, d.createdAt).UTC().Format(time.RFC3339)}
	switch {
	case d.afterKind != "":
		item.After = &protocol.DueAfter{}
		switch d.afterKind {
		case "reply":
			item.After.Reply = d.afterRef
		case "change_step":
			item.After.ChangeStep = d.afterRef
		case "ask_closed":
			item.After.AskClosed = d.afterRef
		}
	default:
		item.Due = time.Unix(0, d.dueAt).UTC().Format(time.RFC3339)
	}
	if d.wokenAt > 0 {
		item.WokenAt = time.Unix(0, d.wokenAt).UTC().Format(time.RFC3339)
	}
	return item
}

// SessionDue runs agent_due for one session. An item belongs to the session
// that added it: list shows its open items, and done refuses another session's.
func (s *Store) SessionDue(session string, q protocol.DueArgs) (protocol.DueResult, error) {
	now := s.now()
	switch q.Action {
	case protocol.DueActionAdd:
		return s.addDue(session, q, now)
	case protocol.DueActionList:
		rows, err := s.db.Query(`SELECT `+sessionDueColumns+` FROM session_dues WHERE session_id=? AND status<>? ORDER BY created_at,id`, session, dueStatusDone)
		if err != nil {
			return protocol.DueResult{}, err
		}
		defer rows.Close()
		var out protocol.DueResult
		for rows.Next() {
			d, err := scanSessionDue(rows)
			if err != nil {
				return protocol.DueResult{}, err
			}
			out.Items = append(out.Items, dueItemOf(d))
		}
		return out, rows.Err()
	case protocol.DueActionDone:
		res, err := s.db.Exec(`UPDATE session_dues SET status=? WHERE id=? AND session_id=? AND status<>?`, dueStatusDone, q.ID, session, dueStatusDone)
		if err != nil {
			return protocol.DueResult{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var mine int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_dues WHERE id=? AND session_id=?`, q.ID, session).Scan(&mine); err != nil {
				return protocol.DueResult{}, err
			}
			if mine == 0 {
				return protocol.DueResult{}, reject("no due item %q of this session", q.ID)
			}
		}
		d, err := scanSessionDue(s.db.QueryRow(`SELECT `+sessionDueColumns+` FROM session_dues WHERE id=?`, q.ID))
		if err != nil {
			return protocol.DueResult{}, err
		}
		return protocol.DueResult{Items: []protocol.DueItem{dueItemOf(d)}}, nil
	}
	return protocol.DueResult{}, reject("action must be add, list or done")
}

func (s *Store) addDue(session string, q protocol.DueArgs, now time.Time) (protocol.DueResult, error) {
	d := sessionDue{id: "", session: session, what: q.What, blocker: q.Blocker, status: dueStatusPending, createdAt: now.UnixNano()}
	if d.blocker == "" {
		d.blocker = protocol.DueBlockerNone
	}
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.DueResult{}, err
	}
	defer tx.Rollback()
	if d.blocker != protocol.DueBlockerNone && d.blocker != protocol.DueBlockerOwner {
		var known bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, d.blocker).Scan(&known); err != nil {
			return protocol.DueResult{}, err
		}
		if !known {
			return protocol.DueResult{}, reject("blocker %q is not owner, none or a known session", d.blocker)
		}
	}
	if q.After == nil {
		due, err := protocol.ParseFollowUp(q.Due, now)
		if err != nil {
			return protocol.DueResult{}, reject("%v", err)
		}
		d.dueAt = due.UnixNano()
	} else {
		var exists bool
		switch {
		case q.After.Reply != "":
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE id=?)`, q.After.Reply).Scan(&exists)
			d.afterKind, d.afterRef = "reply", q.After.Reply
		case q.After.ChangeStep != "":
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_changes WHERE id=?)`, q.After.ChangeStep).Scan(&exists)
			d.afterKind, d.afterRef = "change_step", q.After.ChangeStep
		default:
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM inbox_items WHERE id=?)`, q.After.AskClosed).Scan(&exists)
			d.afterKind, d.afterRef = "ask_closed", q.After.AskClosed
		}
		if err != nil {
			return protocol.DueResult{}, err
		}
		if !exists {
			return protocol.DueResult{}, reject("after %s %q is not known", d.afterKind, d.afterRef)
		}
	}
	d.id = protocol.NewID("d")
	if _, err := tx.Exec(`INSERT INTO session_dues(id,session_id,what,due_at,after_kind,after_ref,blocker,status,woken_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,0,?)`, d.id, d.session, d.what, d.dueAt, d.afterKind, d.afterRef, d.blocker, d.status, d.createdAt); err != nil {
		return protocol.DueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.DueResult{}, err
	}
	return protocol.DueResult{Items: []protocol.DueItem{dueItemOf(d)}}, nil
}

// dueEventHappened says whether the event an after item waits for has happened.
func dueEventHappened(q askDB, d sessionDue) (bool, error) {
	switch d.afterKind {
	case "reply":
		var conversation, recipient string
		if err := q.QueryRow(`SELECT conversation_id, recipient FROM messages WHERE id=?`, d.afterRef).Scan(&conversation, &recipient); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		return eventHappened(q, followUp{event: "reply", messageID: d.afterRef, conversationID: conversation, recipient: recipient, createdAt: d.createdAt})
	case "change_step":
		return eventHappened(q, followUp{event: "change", changeID: d.afterRef, createdAt: d.createdAt})
	}
	var closed bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM inbox_items WHERE id=? AND status IN ('answered','closed','carried_out'))`, d.afterRef).Scan(&closed)
	return closed, err
}

// dueSessionActive says whether the session showed activity since it was woken:
// a message it sent, or harness or announce activity (the same signal the
// stall check reads).
func dueSessionActive(q askDB, d sessionDue) (bool, error) {
	var active bool
	err := q.QueryRow(`SELECT (MAX(s.last_active, s.harness_activity) > ?)
		OR EXISTS(SELECT 1 FROM messages m WHERE m.sender=s.id AND m.created_at>?)
		FROM sessions s WHERE s.id=?`, d.wokenAt, d.wokenAt, d.session).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
}

// raiseDueAsk raises the one chief ask of an item. why says what went wrong.
func (s *Store) raiseDueAsk(tx askDB, d sessionDue, why string) error {
	var node string
	_ = tx.QueryRow(`SELECT COALESCE(repo_project,'') FROM sessions WHERE id=?`, d.session).Scan(&node)
	text := fmt.Sprintf("%s (%s) %s. Item %s: %s. Due: %s.",
		sessionLabel(tx, d.session), d.session, why, d.id, d.what, d.expectedText())
	_, err := s.createInboxItem(tx, protocol.InboxItem{
		Kind: protocol.AskKindQuestion, SessionID: d.session, Node: node, NextWho: protocol.AskNextChief,
		SourceRef: "due:" + d.id, Text: text, Options: []string{"wait", "close"},
	})
	return err
}

// checkSessionDues runs on the minute tick. It wakes the session of each item
// that has fallen due, or whose event has happened, and returns the wake
// messages for the caller to dispatch. A woken session that shows no activity
// for dueIdleAfter gets one chief ask. Owner or session blocks get their one
// chief ask when they fall due, with no wake.
func (s *Store) checkSessionDues() ([]protocol.Message, error) {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT `+sessionDueColumns+` FROM session_dues
		WHERE (status=? AND (after_kind<>'' OR due_at<=?)) OR (status=? AND woken_at<=?)
		ORDER BY created_at,id`,
		dueStatusPending, now.UnixNano(), dueStatusWoken, now.Add(-dueIdleAfter).UnixNano())
	if err != nil {
		return nil, err
	}
	var open []sessionDue
	for rows.Next() {
		d, err := scanSessionDue(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		open = append(open, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var wakes []protocol.Message
	for _, d := range open {
		if d.status == dueStatusWoken {
			active, err := dueSessionActive(tx, d)
			if err != nil {
				return nil, err
			}
			if active {
				if _, err := tx.Exec(`UPDATE session_dues SET status=? WHERE id=?`, dueStatusActive, d.id); err != nil {
					return nil, err
				}
				continue
			}
			why := fmt.Sprintf("was woken at %s and has shown no activity since", time.Unix(0, d.wokenAt).UTC().Format(time.RFC3339))
			if err := s.raiseDueAsk(tx, d, why); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE session_dues SET status=? WHERE id=?`, dueStatusRaised, d.id); err != nil {
				return nil, err
			}
			continue
		}
		if d.afterKind != "" {
			met, err := dueEventHappened(tx, d)
			if err != nil {
				return nil, err
			}
			if !met {
				continue
			}
		}
		switch d.blocker {
		case protocol.DueBlockerNone:
			m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"), From: "agent-net", FromName: "agent-net",
				FromHarness: "agent-net", To: d.session, Type: protocol.TypeNotification,
				Body:           fmt.Sprintf("[agent-net] Due now: %s (item %s). Do this step, then call agent_due with action done and id %s. If it waits on someone, say who.", d.what, d.id, d.id),
				IdempotencyKey: "due:" + d.id, CreatedAt: now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
			if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
				VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`,
				m.ID, m.ConversationID, "", m.From, m.To, m.Type, m.Body, m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
				return nil, err
			}
			s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net due="+d.id)
			if _, err := tx.Exec(`UPDATE session_dues SET status=?, woken_at=? WHERE id=?`, dueStatusWoken, now.UnixNano(), d.id); err != nil {
				return nil, err
			}
			wakes = append(wakes, m)
		case protocol.DueBlockerOwner:
			if err := s.raiseDueAsk(tx, d, "has a due item that waits on the owner"); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE session_dues SET status=? WHERE id=?`, dueStatusRaised, d.id); err != nil {
				return nil, err
			}
		default:
			if err := s.raiseDueAsk(tx, d, fmt.Sprintf("has a due item that waits on %s (%s)", sessionLabel(tx, d.blocker), d.blocker)); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE session_dues SET status=? WHERE id=?`, dueStatusRaised, d.id); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return wakes, nil
}
