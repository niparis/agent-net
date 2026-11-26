package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/niparis/agent-net/internal/protocol"
)

// stoodDownMigration adds the stood-down mark to sessions and the redirect
// note to messages. A session is stood down when the owner or the CTO stopped
// it, or when its merger or lead role moved to another session. A message
// never wakes such a session: it goes to its successor, or else to its
// project lead. Appended at the end of the migration list and never edited
// once it ships.
const stoodDownMigration = `SELECT 1 /* ` + stoodDownMarker + ` */;`

// stoodDownMarker names stoodDownMigration for the column step of migrate.
const stoodDownMarker = "stood down session columns"

// stoodDownColumns are the columns stoodDownMigration adds.
var stoodDownColumns = []migrationColumn{
	{table: "sessions", name: "stood_down", spec: `TEXT NOT NULL DEFAULT ''`},
	{table: "sessions", name: "stood_down_for", spec: `TEXT NOT NULL DEFAULT ''`},
	{table: "messages", name: "redirect_note", spec: `TEXT NOT NULL DEFAULT ''`},
}

// Why a session was stood down. The value is stored in sessions.stood_down.
const (
	stoodDownStopped = "stopped"
	stoodDownMerger  = "merger"
	stoodDownLead    = "lead"
)

// standDownTx marks a session stood down for why, with the session that took
// over its work, or none. A session that is stood down already keeps its
// first reason and successor: the first step down is the one that named who
// took over. The mark is cleared when the session comes online again
// (upsertSession), since only the owner or the CTO starts a stopped session.
func standDownTx(q interface {
	Exec(string, ...any) (sql.Result, error)
}, session, why, successor string) error {
	if session == "" || session == successor {
		return nil
	}
	_, err := q.Exec(`UPDATE sessions SET stood_down=?, stood_down_for=? WHERE id=? AND stood_down='' AND worker=''`, why, successor, session)
	return err
}

// clearStoodDownTx ends the mark of a session the owner or the CTO gives a
// role again: his newest word is that it works.
func clearStoodDownTx(tx *sql.Tx, session string) error {
	if session == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE sessions SET stood_down='', stood_down_for='' WHERE id=? AND stood_down<>''`, session)
	return err
}

// MarkSessionStopped records that the owner or the CTO stopped a session on
// purpose. Only the machine that holds the session may say so.
func (s *Store) MarkSessionStopped(nodeID, session string) error {
	var node string
	err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, session).Scan(&node)
	if errors.Is(err, sql.ErrNoRows) {
		return reject("unknown session %s", session)
	}
	if err != nil {
		return err
	}
	if node != nodeID {
		return reject("only the machine that holds a session can mark it stopped")
	}
	return standDownTx(s.db, session, stoodDownStopped, "")
}

// WakeBlocked tells whether nothing may wake the session: it is archived, or
// stood down. A lookup error blocks the wake.
func (s *Store) WakeBlocked(session string) bool {
	var blocked bool
	err := s.db.QueryRow(`SELECT archived_at<>0 OR stood_down<>'' FROM sessions WHERE id=?`, session).Scan(&blocked)
	return err != nil || blocked
}

// stoodDown reads why a session was stood down and who took over its work.
// why is empty when it was not.
type stoodDown struct {
	why, successor, project, name string
	online, archived              bool
}

func readStoodDown(q queryer, session string) (stoodDown, bool, error) {
	var d stoodDown
	err := q.QueryRow(`SELECT s.stood_down, s.stood_down_for, `+sessionRepoSQL+`,
		COALESCE(NULLIF(s.name,''),NULLIF(s.title,''),s.id), s.online<>0, s.archived_at<>0
		FROM sessions s WHERE s.id=?`, session).Scan(&d.why, &d.successor, &d.project, &d.name, &d.online, &d.archived)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// stoodDownTarget finds where a message for a stood-down offline session goes:
// the session that took over its work, followed through replacements, or else
// the lead session of its project. A successor that is itself stood down and
// offline passes the message on in the same way. target is empty when the
// session is not stood down, or when no other session can take the message
// (the session is its own project's lead, or the chain loops); the message
// then waits, and nothing wakes the session.
func (s *Store) stoodDownTarget(session string) (target, note string, err error) {
	first, ok, err := readStoodDown(s.db, session)
	if err != nil || !ok || first.why == "" || first.online {
		return "", "", err
	}
	seen := map[string]bool{session: true}
	d := first
	for range maxReplaceChain {
		next, asLead := "", false
		if d.successor != "" {
			if next, _, err = liveSession(s.db, d.successor); err != nil {
				return "", "", err
			}
			// An archived successor took over nothing.
			if nd, ok, err := readStoodDown(s.db, next); err != nil {
				return "", "", err
			} else if !ok || nd.archived {
				next = ""
			}
		}
		if next == "" || seen[next] {
			next, err = s.projectLeadAddress(d.project)
			if err != nil {
				return "", "", err
			}
			asLead = true
		}
		if next == "" || seen[next] {
			return "", "", nil
		}
		nd, ok, err := readStoodDown(s.db, next)
		if err != nil {
			return "", "", err
		}
		if !ok || nd.archived {
			return "", "", nil
		}
		if nd.why == "" || nd.online {
			return next, redirectNote(session, first, nd.name, next, asLead), nil
		}
		seen[next] = true
		d = nd
	}
	return "", "", nil
}

// projectLeadAddress is where a message for a project's lead goes: the lead
// session that lead routing picks, or empty when the project has none.
func (s *Store) projectLeadAddress(project string) (string, error) {
	if project == "" {
		return "", nil
	}
	id, err := leadAddressSessionQ(s.db, "repo", project, s.now())
	var rej Rejection
	if errors.As(err, &rej) || errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// redirectNote is agent-net's line on a message that went to another session
// than the one it was sent to: who it was for, why it moved, and why this
// session got it.
func redirectNote(session string, d stoodDown, toName, to string, asLead bool) string {
	role := "the session that took over its work"
	if asLead {
		role = "the lead of its project"
	}
	return fmt.Sprintf("This message was sent to %s (%s), which is offline and is not woken because %s. agent-net delivered it to you, %s (%s), as %s. Act on it or pass it on.",
		d.name, session, d.reason(), toName, to, role)
}

// reason says in words why nothing wakes the session.
func (d stoodDown) reason() string {
	switch d.why {
	case stoodDownStopped:
		return "the owner or the CTO stopped it"
	case stoodDownMerger:
		if d.successor == "" {
			return "it is no longer the project's merger"
		}
		return "its merger role moved to another session"
	case stoodDownLead:
		return "its lead role moved to another session"
	}
	if d.archived {
		return "it was archived"
	}
	return ""
}

// redirectReceipt is the sender's line for a message that dispatch moved
// from a stood-down session, or empty when it did not move.
func (s *Store) redirectReceipt(m protocol.Message) (string, error) {
	var to string
	var moved bool
	err := s.db.QueryRow(`SELECT recipient, redirect_note<>'' FROM messages WHERE id=?`, m.ID).Scan(&to, &moved)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!moved || to == m.To) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	d, _, err := readStoodDown(s.db, m.To)
	if err != nil {
		return "", err
	}
	name, err := s.sessionDisplayName(to)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = to
	}
	return fmt.Sprintf("%s is offline and is not woken because %s; agent-net gave the message to %s (%s).", d.name, d.reason(), name, to), nil
}

// redirectStoodDown moves a message that waits for a stood-down offline
// session to the session stoodDownTarget names, and records the note the
// recipient sees. moved is false when the message stays where it is.
func (s *Store) redirectStoodDown(m protocol.Message) (protocol.Message, bool, error) {
	target, note, err := s.stoodDownTarget(m.To)
	// A message is never handed back to its own sender: a lead's reply to a
	// stood-down session that falls to that same lead waits instead.
	if err != nil || target == "" || target == m.To || target == m.From {
		return m, false, err
	}
	res, err := s.db.Exec(`UPDATE messages SET recipient=?, redirect_note=? WHERE id=? AND recipient=? AND state IN `+undeliveredStates,
		target, note, m.ID, m.To)
	if err != nil {
		return m, false, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return m, false, err
	}
	m.To = target
	return m, true, nil
}

// redirectNoteOf is the stored redirect note of a message, or empty.
func (s *Store) redirectNoteOf(id string) (string, error) {
	var note string
	err := s.db.QueryRow(`SELECT redirect_note FROM messages WHERE id=?`, id).Scan(&note)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return note, err
}

// pendingFor lists the messages that still wait for a session.
func (s *Store) pendingFor(session string) ([]protocol.Message, error) {
	rows, err := s.db.Query(msgSelect+` WHERE m.recipient=? AND m.state='stored' AND m.expires_at>? ORDER BY m.created_at,m.rowid`,
		session, s.now().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// standDownStopped marks a session stopped for its machine and passes the
// messages that already wait for it on, as dispatch does for later ones.
func (g *Gateway) standDownStopped(ctx context.Context, nodeID, session string) error {
	if err := g.store.MarkSessionStopped(nodeID, session); err != nil {
		return err
	}
	g.dispatchWaitingFor(ctx, session)
	return nil
}

// dispatchWaitingFor dispatches the messages that wait for a session, so those
// for a session just stood down move to its successor at once.
func (g *Gateway) dispatchWaitingFor(ctx context.Context, session string) {
	pending, err := g.store.pendingFor(session)
	if err != nil {
		g.logLine("error", "where", "dispatch pending", "session", session, "err", err)
		return
	}
	for _, m := range pending {
		g.dispatch(ctx, m)
	}
}
