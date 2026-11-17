package daemon

import (
	"database/sql"
	"errors"
	"time"
)

// The CTO keeps one conversation per harness in session_conversations
// (decision 0171). The row says which native conversation the harness holds
// for the CTO and when it was last used. A switch resumes a conversation used
// in the last ctoConversationFresh only; an older one is dropped, so the new
// conversation starts from the handover note and not from days-old context.

// ctoConversationSession is the CTO's key in session_conversations until it
// has a network session ID. From then on the key is that ID, like any
// session's, and registration moves the rows (rekeyCTOConversations).
const ctoConversationSession = "cto"

// ctoKeySQL is the CTO's key in a statement: its network session ID, or the
// early key before it has one.
const ctoKeySQL = `COALESCE(NULLIF((SELECT session_id FROM cto_network WHERE id=1),''),'` + ctoConversationSession + `')`

// ctoConversationFresh is how long a conversation stays resumable after its
// last turn.
const ctoConversationFresh = 24 * time.Hour

// sqlExecer is what a conversation write needs: the database or a transaction.
type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// migrateCTOState creates session_conversations and copies the CTO's two old
// columns into it, once. The copied rows get updated_at 0, so they count as
// stale until the harness is used again. The old columns stay in the table and
// are not read any more. It also adds the columns the handover and the CTO's
// skill keep their state in (decisions 0171).
func migrateCTOState(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_conversations (
 session_id TEXT NOT NULL, harness TEXT NOT NULL, native_id TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(session_id, harness))`); err != nil {
		return err
	}
	for _, col := range []struct{ name, spec string }{
		{"conversations_migrated", "conversations_migrated INTEGER NOT NULL DEFAULT 0"},
		{"handover_note", "handover_note TEXT NOT NULL DEFAULT ''"},
		{"setup_skill_hash", "setup_skill_hash TEXT NOT NULL DEFAULT ''"},
		{"setup_skill_listed", "setup_skill_listed INTEGER NOT NULL DEFAULT 0"},
	} {
		var has int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cto') WHERE name = ?`, col.name).Scan(&has); err != nil {
			return err
		}
		if has == 0 {
			if _, err := db.Exec(`ALTER TABLE cto ADD COLUMN ` + col.spec); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO cto(id,session_id) VALUES(1,'')`); err != nil {
		return err
	}
	var done int
	if err := db.QueryRow(`SELECT conversations_migrated FROM cto WHERE id=1`).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return rekeyOpenState(db)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO session_conversations(session_id,harness,native_id,updated_at)
SELECT ?,'opencode',session_id,0 FROM cto WHERE id=1 AND session_id<>''`, ctoConversationSession); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO session_conversations(session_id,harness,native_id,updated_at)
SELECT ?,'claude',claude_session_id,0 FROM cto WHERE id=1 AND claude_session_id<>''`, ctoConversationSession); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE cto SET conversations_migrated=1 WHERE id=1`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return rekeyOpenState(db)
}

// rekeyOpenState moves rows under the early key to the CTO's network session
// ID when it has one. It is a no-op once they have moved.
func rekeyOpenState(db *sql.DB) error {
	var id string
	err := db.QueryRow(`SELECT session_id FROM cto_network WHERE id=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && id == "") {
		return nil
	}
	if err != nil {
		return err
	}
	return rekeyConversations(db, id)
}

// rekeyCTOConversations moves the CTO's conversations to its network session
// ID, from the early key and from the ID it had before.
func (d *Daemon) rekeyCTOConversations(to string, before ...string) error {
	return rekeyConversations(d.st.db, to, before...)
}

// rekeyConversations copies each row of the old keys to the new key and drops
// the old rows. A row already under the new key wins, so a move never
// overwrites a newer conversation. The age moves with the row.
func rekeyConversations(db *sql.DB, to string, before ...string) error {
	from := []string{ctoConversationSession}
	for _, id := range before {
		if id != "" && id != to {
			from = append(from, id)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, old := range from {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO session_conversations(session_id,harness,native_id,updated_at)
SELECT ?,harness,native_id,updated_at FROM session_conversations WHERE session_id=?`, to, old); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM session_conversations WHERE session_id=?`, old); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ctoConversation is the native conversation the CTO's harness holds, or ""
// when there is none.
func (d *Daemon) ctoConversation(harness string) (string, error) {
	var id string
	err := d.st.db.QueryRow(`SELECT native_id FROM session_conversations WHERE session_id=`+ctoKeySQL+` AND harness=?`, harness).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// setCTOConversation stores the native conversation of one harness and marks
// it used now. An empty native ID ends the conversation.
func (d *Daemon) setCTOConversation(harness, native string) error {
	return setCTOConversationExec(d.st.db, d.ctoNowTime(), harness, native)
}

// setCTOConversationExec is setCTOConversation inside a caller's transaction.
func setCTOConversationExec(x sqlExecer, now time.Time, harness, native string) error {
	_, err := x.Exec(`INSERT INTO session_conversations(session_id,harness,native_id,updated_at) VALUES(`+ctoKeySQL+`,?,?,?)
ON CONFLICT(session_id,harness) DO UPDATE SET native_id=excluded.native_id, updated_at=excluded.updated_at`,
		harness, native, now.UnixNano())
	return err
}

// touchCTOConversation marks a live conversation as used now. Each turn calls
// it, so a switch back within a day resumes it.
func (d *Daemon) touchCTOConversation(harness string) error {
	_, err := d.st.db.Exec(`UPDATE session_conversations SET updated_at=? WHERE session_id=`+ctoKeySQL+` AND harness=? AND native_id<>''`,
		d.ctoNowTime().UnixNano(), harness)
	return err
}

// ctoSwitchConversation runs when a switch goes to harness. The harness's
// conversation resumes only when it was used in the last ctoConversationFresh.
// Otherwise it is dropped here, and the next start opens a new conversation.
func (d *Daemon) ctoSwitchConversation(harness string) error {
	var native string
	var updated int64
	err := d.st.db.QueryRow(`SELECT native_id, updated_at FROM session_conversations WHERE session_id=`+ctoKeySQL+` AND harness=?`, harness).Scan(&native, &updated)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && native == "") {
		return nil
	}
	if err != nil {
		return err
	}
	age := d.ctoNowTime().UnixNano() - updated
	if age >= 0 && age <= int64(ctoConversationFresh) {
		return nil
	}
	return d.setCTOConversation(harness, "")
}
