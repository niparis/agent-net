package gateway

import (
	"database/sql"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// RememberOwnerPrompt keeps only the latest prompt of a session; it is never logged.
func (s *Store) RememberOwnerPrompt(session, prompt string) error {
	// Claude delivers peer messages, task notices and reminders as user turns.
	// They are not the owner's words, so they are not kept. The owner's earlier
	// prompt stays: a message that lands between the owner's prompt and the
	// agent's send must not erase what the owner asked.
	if protocol.InjectedClaudePrompt(prompt) {
		return nil
	}
	r := []rune(prompt)
	if len(r) > 300 {
		prompt = string(r[:300])
	}
	_, err := s.db.Exec(`UPDATE sessions SET owner_prompt=?, owner_prompt_at=? WHERE id=?`, prompt, s.now().UnixNano(), session)
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

// signalRequest keeps the owner's prompt on the first request its session
// sends after the prompt, when the prompt names the recipient, its project or
// its name. The prompt explains at most that first send, so it is cleared
// either way.
func (s *Store) signalRequest(tx *sql.Tx, m protocol.Message) error {
	var prompt, from, to, name, sessionName, project string
	var at int64
	err := tx.QueryRow(`SELECT a.owner_prompt,a.owner_prompt_at,COALESCE(NULLIF(a.repo_project,''),ap.project,''),COALESCE(NULLIF(b.repo_project,''),bp.project,''),COALESCE(bp.name,''),COALESCE(b.name,''),COALESCE(bp.project,'') FROM sessions a JOIN sessions b ON b.id=? LEFT JOIN profiles ap ON ap.session_id=a.id LEFT JOIN profiles bp ON bp.session_id=b.id WHERE a.id=?`, m.To, m.From).Scan(&prompt, &at, &from, &to, &name, &sessionName, &project)
	if err == sql.ErrNoRows || (err == nil && prompt == "") {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE sessions SET owner_prompt='', owner_prompt_at=0 WHERE id=?`, m.From); err != nil {
		return err
	}
	if m.CreatedAt.UnixNano()-at < 0 || m.CreatedAt.UnixNano()-at > int64(10*time.Minute) {
		return nil
	}
	if !mentionsName(prompt, to) && !mentionsName(prompt, project) && !mentionsName(prompt, name) && !mentionsName(prompt, sessionName) {
		return nil
	}
	_, err = tx.Exec(`UPDATE messages SET owner_prompt=? WHERE id=?`, prompt, m.ID)
	return err
}

// Signals lists, newest first, the requests that carry the owner's prompt and
// the outcomes of requests, for the last 90 days. An outcome shows its request's
// prompt when the request has one.
func (s *Store) Signals() ([]protocol.CharterSignal, error) {
	since := s.now().Add(-90 * 24 * time.Hour).UnixNano()
	rows, err := s.db.Query(`SELECT base.src,base.dst,base.mid,base.prompt,COALESCE(o.reply_id,''),COALESCE(o.outcome,''),COALESCE(o.reason,''),COALESCE(o.redirect_to,''),base.at FROM (
		SELECT COALESCE(NULLIF(a.repo_project,''),ap.project,'') AS src,COALESCE(NULLIF(b.repo_project,''),bp.project,'') AS dst,m.id AS mid,m.owner_prompt AS prompt,m.created_at AS at
		FROM messages m JOIN sessions a ON a.id=m.sender JOIN sessions b ON b.id=m.recipient LEFT JOIN profiles ap ON ap.session_id=a.id LEFT JOIN profiles bp ON bp.session_id=b.id
		WHERE m.owner_prompt<>'' AND m.created_at>=?
		UNION ALL
		SELECT o.source,o.target,o.message_id,'',o.at FROM graph_outcomes o
		WHERE o.at>=? AND NOT EXISTS (SELECT 1 FROM messages p WHERE p.id=o.message_id AND p.owner_prompt<>'')
	) base LEFT JOIN graph_outcomes o ON o.message_id=base.mid
	ORDER BY base.at DESC, o.at DESC LIMIT 200`, since, since)
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
