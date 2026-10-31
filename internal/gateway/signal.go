package gateway

import (
	"database/sql"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// RememberOwnerPrompt keeps only the latest prompt; it is never logged.
func (s *Store) RememberOwnerPrompt(session, prompt string) error {
	r := []rune(prompt)
	if len(r) > 300 {
		prompt = string(r[:300])
	}
	_, err := s.db.Exec(`INSERT INTO signal_prompts(session_id,prompt,at) VALUES(?,?,?) ON CONFLICT(session_id) DO UPDATE SET prompt=excluded.prompt,at=excluded.at`, session, prompt, s.now().UnixNano())
	return err
}

func mentionsName(text, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	text = strings.ToLower(text)
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], name)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(name)
		boundary := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' }
		before := []rune(text[:i])
		after := []rune(text[end:])
		if (len(before) == 0 || boundary(before[len(before)-1])) && (len(after) == 0 || boundary(after[0])) {
			return true
		}
		offset = end
	}
	return false
}

func (s *Store) signalRequest(tx *sql.Tx, m protocol.Message) error {
	var prompt, from, to, name string
	var at int64
	err := tx.QueryRow(`SELECT p.prompt,p.at,COALESCE(NULLIF(a.repo_project,''),ap.project,''),COALESCE(NULLIF(b.repo_project,''),bp.project,''),COALESCE(NULLIF(bp.name,''),b.name,'') FROM signal_prompts p JOIN sessions a ON a.id=p.session_id JOIN sessions b ON b.id=? LEFT JOIN profiles ap ON ap.session_id=a.id LEFT JOIN profiles bp ON bp.session_id=b.id WHERE p.session_id=?`, m.To, m.From).Scan(&prompt, &at, &from, &to, &name)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if m.CreatedAt.UnixNano()-at < 0 || m.CreatedAt.UnixNano()-at > int64(10*time.Minute) {
		return nil
	}
	matched := mentionsName(prompt, from) || mentionsName(prompt, to) || mentionsName(prompt, name)
	if !matched {
		rows, e := tx.Query(`SELECT name FROM graph_nodes WHERE name<>? UNION SELECT COALESCE(NULLIF(p.name,''),s.name,'') FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id<>? UNION SELECT COALESCE(NULLIF(s.repo_project,''),p.project,'') FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id<>?`, from, m.From, m.From)
		if e != nil {
			return e
		}
		for rows.Next() {
			var candidate string
			if e = rows.Scan(&candidate); e != nil {
				rows.Close()
				return e
			}
			if candidate != from && mentionsName(prompt, candidate) {
				matched = true
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	if !matched {
		return nil
	}
	_, err = tx.Exec(`INSERT INTO charter_signals(message_id,source,target,prompt,at) VALUES(?,?,?,?,?)`, m.ID, from, to, prompt, m.CreatedAt.UnixNano())
	if err != nil {
		return err
	}
	return s.learnOwner(tx, from, to, m.ID, prompt, m.CreatedAt.UnixNano())
}

func (s *Store) signalOutcome(tx *sql.Tx, m protocol.Message, p protocol.MsgSend) error {
	// Keep outcomes even without a prompt: redirects are evidence too.
	_, err := tx.Exec(`INSERT OR IGNORE INTO charter_signals(message_id,source,target,prompt,at)
 SELECT q.id,COALESCE(NULLIF(a.repo_project,''),ap.project,''),COALESCE(NULLIF(b.repo_project,''),bp.project,''),'',? FROM messages q JOIN sessions a ON a.id=q.sender JOIN sessions b ON b.id=q.recipient LEFT JOIN profiles ap ON ap.session_id=a.id LEFT JOIN profiles bp ON bp.session_id=b.id WHERE q.id=?`, m.CreatedAt.UnixNano(), p.ParentID)
	if err != nil {
		return err
	}
	return s.learnOutcome(tx, m.ID, m.CreatedAt.UnixNano())
}

func (s *Store) Signals() ([]protocol.CharterSignal, error) {
	rows, err := s.db.Query(`SELECT s.source,s.target,s.message_id,s.prompt,COALESCE(o.reply_id,''),COALESCE(o.outcome,''),COALESCE(o.reason,''),COALESCE(o.redirect_to,''),s.at FROM charter_signals s LEFT JOIN graph_outcomes o ON o.message_id=s.message_id WHERE s.at>=? ORDER BY s.at DESC, o.at DESC LIMIT 200`, s.now().Add(-90*24*time.Hour).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CharterSignal{}
	for rows.Next() {
		var v protocol.CharterSignal
		if err = rows.Scan(&v.From, &v.To, &v.MessageID, &v.Prompt, &v.ReplyID, &v.Outcome, &v.Reason, &v.RedirectTo, &v.At); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
