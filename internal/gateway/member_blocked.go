package gateway

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// Blocked members (decision 0111). A member of a team with asks_to=lead can be
// stuck without asking anything: it announces "blocked on the owner" and
// waits. The ask routing of decision 0100 never runs for that, so the lead
// heard nothing. Three things close the gap:
//
//   - the announce itself: a member that announces a blocked or waiting task
//     sends its lead one message (blockedMemberNotice);
//   - the lead's loop tick lists the members that are blocked or waiting, and
//     the workers they started (loopMembers, in member_loop.go);
//   - the views add a note to a task line that names an ended worker
//     (applyEndedWorkerNotes, in member_worker_note.go).

// blockedPhrases are the words, anywhere in an announce, that say the session
// waits for someone. The idle check's answer "blocked ..." at the start of an
// announce counts too (parseCheckAnswer).
var blockedPhrases = []string{"blocked on", "waiting on"}

// notBlockedBefore are the words that turn a phrase around: "not blocked on
// anything", "no longer waiting on the owner", "was blocked on CI".
var notBlockedBefore = []string{"not", "no", "no longer", "never", "was", "were", "isn't", "wasn't", "aren't"}

// taskReadsBlocked tells whether the text of an announce says its session is
// blocked or waiting. An announce that starts with "done" never does.
func taskReadsBlocked(task string) bool {
	task = strings.TrimSpace(task)
	kind, _ := parseCheckAnswer(task)
	switch kind {
	case "blocked":
		return true
	case "done":
		return false
	}
	lower := strings.ToLower(task)
	for _, phrase := range blockedPhrases {
		for from := 0; from < len(lower); {
			i := strings.Index(lower[from:], phrase)
			if i < 0 {
				break
			}
			i += from
			from = i + len(phrase)
			if phraseStandsAlone(lower, i, from) {
				return true
			}
		}
	}
	return false
}

// phraseStandsAlone checks that the phrase at lower[start:end] is a whole
// phrase ("unblocked on" is not) and is not turned around by a word before it.
func phraseStandsAlone(lower string, start, end int) bool {
	if start > 0 {
		if r := []rune(lower[:start]); unicode.IsLetter(r[len(r)-1]) || unicode.IsDigit(r[len(r)-1]) {
			return false
		}
	}
	if end < len(lower) {
		if r := []rune(lower[end:])[0]; unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	before := strings.TrimRight(lower[:start], " \t,;:-(")
	for _, w := range notBlockedBefore {
		if before == w || strings.HasSuffix(before, " "+w) {
			return false
		}
	}
	return true
}

// blockedReading is what an announce says when it reads as blocked or waiting:
// the task, plus the text of the wait the announce set. ok is false when it
// reads as neither.
func blockedReading(task string, waiting *protocol.Waiting) (text string, ok bool) {
	task = strings.TrimSpace(task)
	w := protocol.CleanWaiting(waiting)
	if w == nil && !taskReadsBlocked(task) {
		return "", false
	}
	text = task
	if w != nil && w.Text != "" && !strings.Contains(task, w.Text) {
		if text != "" {
			text += " "
		}
		text += "(waiting: " + w.Text + ")"
	}
	return text, text != ""
}

// memberLabel is how a notice and a loop tick name a member session: its agent,
// then its own name. The id follows in a notice, so the lead can message it.
func memberLabel(q interface{ QueryRow(string, ...any) *sql.Row }, session string) string {
	var agent, name string
	if q.QueryRow(`SELECT COALESCE((SELECT display_name FROM agents WHERE id=s.agent_id),''),COALESCE(NULLIF(p.name,''),s.name,'')
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&agent, &name) != nil {
		return session
	}
	return firstNonEmpty(agent, name, session)
}

func clockText(unixNano int64, now time.Time) string {
	t := time.Unix(0, unixNano).UTC()
	if t.Format("2006-01-02") == now.UTC().Format("2006-01-02") {
		return t.Format("15:04") + " UTC"
	}
	return t.Format("2006-01-02 15:04") + " UTC"
}

// waitsOnOwnWorker tells whether an announce that reads as blocked waits only on
// workers the member started and that are still running. Then nothing is sent:
// the member waits on its own work, which is normal progress. Two cases count:
//   - the announce names by session id a running worker the member owns, under
//     any answer ("blocked on worker s_... (PR #79 rework)");
//   - the announce is a "more" answer with a wait phrase, and the member owns a
//     running worker ("more: waiting on worker rework of PR #79").
//
// A wait set in the waiting field always counts, and so does a blocked answer
// that names no running worker of the member.
func (s *Store) waitsOnOwnWorker(p protocol.ProfileAnnounce, task string) (bool, error) {
	if protocol.CleanWaiting(p.Waiting) != nil {
		return false, nil
	}
	task = strings.TrimSpace(task)
	named, err := s.namesOwnRunningWorker(p.SessionID, task)
	if err != nil || named {
		return named, err
	}
	if kind, _ := parseCheckAnswer(task); kind != "more" {
		return false, nil
	}
	n, err := s.owningWorkerCount(p.SessionID)
	return n > 0, err
}

// namesOwnRunningWorker tells whether task contains the session id of a worker
// that member owns and that is online, which is how the idle check counts a
// running worker.
func (s *Store) namesOwnRunningWorker(member, task string) (bool, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE online=1 AND worker<>'' AND json_valid(worker)
		AND json_extract(worker,'$.owner')=?`, member)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		if id != "" && strings.Contains(task, id) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// blockedKey is the fingerprint of a blocked or waiting reading. A wait that
// has a text is told once for that text, whatever task line stands around it:
// a session that rewords its task while it waits has not changed what it
// waits for. Without a wait, the whole reading counts.
func blockedKey(text string, w *protocol.Waiting) string {
	if w != nil && strings.TrimSpace(w.Text) != "" {
		return checkTaskHash("wait: " + w.Text)
	}
	return checkTaskHash(text)
}

// blockedMemberNotice runs after an announce was stored. When the session is a
// member of a team with asks_to=lead and the announce reads as blocked or
// waiting, it tells the team lead once. A wait that names the session's own
// team lead or repo lead is told to that lead in any team (decision 0126). The
// message is a message from agent-net with who, the
// announce text and since when. It returns that message, stored and not yet
// dispatched, or nil when nothing was sent.
//
// One message goes out for each distinct text. A repeat of the same text sends
// nothing. An announce that does not read as blocked forgets the text, so a
// later block is new. When the lead has no session, one Desk log line is
// written instead (the same lead lookup as RedirectOwnerAsk).
func (s *Store) blockedMemberNotice(p protocol.ProfileAnnounce) (*protocol.Message, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	var task string
	var started int64
	err := s.db.QueryRow(`SELECT task,task_started FROM profiles WHERE session_id=?`, p.SessionID).Scan(&task, &started)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text, blocked := blockedReading(task, p.Waiting)
	if !blocked {
		if _, err := s.db.Exec(`DELETE FROM member_blocked_ticks WHERE session_id=?`, p.SessionID); err != nil {
			return nil, err
		}
		_, err := s.db.Exec(`DELETE FROM member_blocked_notices WHERE session_id=?`, p.SessionID)
		return nil, err
	}
	if own, err := s.waitsOnOwnWorker(p, task); err != nil || own {
		return nil, err
	}
	wait := protocol.CleanWaiting(p.Waiting)
	team, leadAgent, pinned, ok, err := s.askerTeam(p.SessionID)
	if err != nil {
		return nil, err
	}
	scope := "Team " + team
	if !ok {
		// A team that asks the owner still tells the lead of a wait that
		// names it (decision 0126). So does a Repo lead.
		if wait == nil {
			return nil, nil
		}
		lead, named, err := s.waitNamesLead(s.db, p.SessionID, wait.Text)
		if err != nil || !named {
			return nil, err
		}
		scope, leadAgent, pinned = lead.scope, lead.agent, lead.pinned
	}
	key := blockedKey(text, wait)
	var seen string
	err = s.db.QueryRow(`SELECT text_hash FROM member_blocked_notices WHERE session_id=?`, p.SessionID).Scan(&seen)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil && seen == key {
		return nil, nil
	}
	leadSession, _, err := s.leadAskSession(leadAgent, pinned)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if started <= 0 {
		started = now.UnixNano()
	}
	who := memberLabel(s.db, p.SessionID)
	line := protocol.NeutralizeMarkers(askFirstLine(text, 300))
	record := func(q askDB) error {
		_, err := q.Exec(`INSERT INTO member_blocked_notices(session_id,text_hash,at) VALUES(?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET text_hash=excluded.text_hash,at=excluded.at`, p.SessionID, key, now.UnixNano())
		return err
	}
	if leadSession == "" || leadSession == p.SessionID {
		var project string
		_ = s.db.QueryRow(`SELECT COALESCE(NULLIF(repo_project,''),'') FROM sessions WHERE id=?`, p.SessionID).Scan(&project)
		kind := ""
		if project != "" {
			kind = "project"
		}
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := s.deskLog(tx, "gateway", kind, project, fmt.Sprintf("%s: %s is blocked or waiting and has no lead session to tell: %s", scope, who, line), "", nil); err != nil {
			return nil, err
		}
		if err := record(tx); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	body := fmt.Sprintf("[agent-net] %s: %s (%s) is blocked or waiting, since %s.\n\nIts announce: %s\n\n"+
		"It asked nothing, so nothing else reached you. Message it if you can unblock it, or ask the owner with agent_owner_ask.",
		scope, who, p.SessionID, clockText(started, now), line)
	id := protocol.NewID("m")
	m := protocol.Message{ID: id, ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: leadSession,
		Type: protocol.TypeNotification, Body: body,
		IdempotencyKey: "member_blocked:" + p.SessionID + ":" + key + ":" + id,
		CreatedAt:      now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return nil, err
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net member_blocked")
	if err := record(tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.logMessage(m)
	return &m, nil
}

// memberBlockedTicksMigration keeps what a lead's loop tick last listed for a
// blocked member (decision 0126): the fingerprint of the wait text, one row per
// session, so the next tick does not list the same wait again. The text itself
// is never stored. The row goes when the member is not blocked any more.
const memberBlockedTicksMigration = `
CREATE TABLE IF NOT EXISTS member_blocked_ticks (
 session_id TEXT PRIMARY KEY, text_hash TEXT NOT NULL);
`
