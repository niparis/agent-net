package gateway

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// The idle check (decision 0036). agent-net asks an idle agent with open work
// whether it is done, at most once per idle period. Slice 3 has no autonomy:
// "more" becomes a Needs you row and "blocked" names the blocker there.
const (
	idleCheckAfter    = 20 * time.Minute
	idleCheckGrace    = 2 * time.Second
	idleCheckDailyCap = 6
	idleCheckBody     = "[agent-net] check: is your task done? Answer with agent_announce: done, more (one line), or blocked on X."
	idleCheckDay      = "2006-01-02"
)

type checkState struct {
	active  int64
	waiting bool
	more    int
	open    int
	msgs    int
	day     string
	count   int
}

func (s *Store) readCheckState(id string) (checkState, error) {
	var st checkState
	var waiting int
	err := s.db.QueryRow(`SELECT check_active,check_waiting,check_more,check_open,check_msgs,check_day,check_day_count
		FROM session_checks WHERE session_id=?`, id).
		Scan(&st.active, &waiting, &st.more, &st.open, &st.msgs, &st.day, &st.count)
	if errors.Is(err, sql.ErrNoRows) {
		return checkState{}, nil
	}
	st.waiting = waiting != 0
	return st, err
}

// checkOpenWork counts what an idle check treats as work left: requests it
// owes an answer, running workers it started, and a non-empty announced task.
func checkOpenWork(a protocol.AgentView, workers int) int {
	n := workers
	if strings.TrimSpace(a.Profile.Task) != "" {
		n++
	}
	for _, p := range a.Pending {
		if !p.Outgoing {
			n++
		}
	}
	return n
}

func (s *Store) checkMessageCount(id string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE (sender=? OR recipient=?) AND sender<>'agent-net'`, id, id).Scan(&n)
	return n, err
}

func (s *Store) ctoSessionID() string {
	var id string
	if err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key='cto_session'`).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (s *Store) owningWorkerCount(id string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE online=1 AND worker<>''
		AND json_valid(worker) AND json_extract(worker,'$.owner')=?`, id).Scan(&n)
	return n, err
}

// checkIdle sends one check to each online session that has been idle long
// enough, has open work and has not been checked since its last activity.
func (s *Store) checkIdle() ([]protocol.Message, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	agents, err := s.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return nil, err
	}
	workers := map[string]int{}
	for _, a := range agents {
		if a.Online && a.Worker != nil {
			workers[a.Worker.Owner]++
		}
	}
	cto := s.ctoSessionID()
	now := s.now()
	today := now.UTC().Format(idleCheckDay)
	var out []protocol.Message
	for _, a := range agents {
		if !a.Online || a.Worker != nil || protocol.IsOwner(a.Harness) || a.Harness == "cto" || a.SessionID == cto {
			continue
		}
		if a.Status != "idle" || a.LastActive == 0 || now.Sub(time.Unix(a.LastActive, 0)) < idleCheckAfter {
			continue
		}
		open := checkOpenWork(a, workers[a.SessionID])
		st, err := s.readCheckState(a.SessionID)
		if err != nil {
			return nil, err
		}
		if open == 0 {
			if st.more != 0 || st.open != 0 || st.msgs != 0 {
				if err := s.resetCheckProgress(a.SessionID); err != nil {
					return nil, err
				}
			}
			continue
		}
		// Only new activity resets the one-check limit.
		if a.LastActive <= st.active {
			continue
		}
		if st.more >= 2 {
			msgs, err := s.checkMessageCount(a.SessionID)
			if err != nil {
				return nil, err
			}
			if open == st.open && msgs == st.msgs {
				continue
			}
			st.more = 0
		}
		count := st.count
		if st.day != today {
			count = 0
		}
		if count >= idleCheckDailyCap {
			continue
		}
		m, err := s.storeCheck(a, today, count+1, st.more, now)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// storeCheck writes the notification and its durable state together. The
// progress counters stay at the last "more" answer, so the next one can tell
// whether anything changed in between.
func (s *Store) storeCheck(a protocol.AgentView, day string, count, more int, now time.Time) (protocol.Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return protocol.Message{}, err
	}
	defer tx.Rollback()
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: a.SessionID,
		Type: protocol.TypeNotification, Body: idleCheckBody,
		IdempotencyKey: fmt.Sprintf("check:%s:%s:%d", a.SessionID, day, count),
		CreatedAt:      now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return protocol.Message{}, err
	}
	if _, err = tx.Exec(`INSERT INTO session_checks(session_id,check_active,check_waiting,check_more,check_day,check_day_count)
		VALUES(?,?,1,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET check_active=excluded.check_active,check_waiting=1,
		check_more=excluded.check_more,check_day=excluded.check_day,check_day_count=excluded.check_day_count`,
		a.SessionID, now.Add(idleCheckGrace).Unix(), more, day, count); err != nil {
		return protocol.Message{}, err
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net check")
	if err = tx.Commit(); err != nil {
		return protocol.Message{}, err
	}
	s.logMessage(m)
	return m, nil
}

// parseCheckAnswer reads the words agent-net asks for at the start of the next
// announce. Anything else is no answer: unknown, never no.
func parseCheckAnswer(task string) (kind, line string) {
	t := strings.TrimSpace(task)
	lower := strings.ToLower(t)
	boundary := func(rest string) bool {
		if rest == "" {
			return true
		}
		r := []rune(rest)[0]
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}
	tail := func(prefix int) string {
		rest := strings.TrimSpace(t[prefix:])
		rest = strings.TrimSpace(strings.TrimLeft(rest, ":-"))
		return protocol.CleanWaitingText(rest)
	}
	switch {
	case strings.HasPrefix(lower, "done") && boundary(t[4:]):
		return "done", ""
	case strings.HasPrefix(lower, "more") && boundary(t[4:]):
		return "more", tail(4)
	case strings.HasPrefix(lower, "blocked on") && boundary(t[len("blocked on"):]):
		return "blocked", tail(len("blocked on"))
	case strings.HasPrefix(lower, "blocked") && boundary(t[len("blocked"):]):
		return "blocked", tail(len("blocked"))
	}
	return "", ""
}

func (s *Store) resetCheckProgress(sessionID string) error {
	_, err := s.db.Exec(`UPDATE session_checks SET check_more=0,check_open=0,check_msgs=0 WHERE session_id=?`, sessionID)
	return err
}

// answerCheck acts on the next announce after a check. It runs for every
// announce; only a session with a check waiting is affected.
func (s *Store) answerCheck(sessionID, task string) error {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	var name, node string
	var st checkState
	var waiting int
	err := s.db.QueryRow(`SELECT COALESCE(c.check_active,0),COALESCE(c.check_waiting,0),
		COALESCE(c.check_more,0),COALESCE(c.check_open,0),COALESCE(c.check_msgs,0),
		COALESCE(c.check_day,''),COALESCE(c.check_day_count,0),
		COALESCE(NULLIF(p.name,''),s.name,''),s.node_id
		FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id
		LEFT JOIN session_checks c ON c.session_id=s.id WHERE s.id=?`, sessionID).
		Scan(&st.active, &waiting, &st.more, &st.open, &st.msgs, &st.day, &st.count, &name, &node)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if waiting == 0 {
		// An announce that does not answer a check is progress: a stalled
		// session has started something new.
		if st.more != 0 || st.open != 0 || st.msgs != 0 {
			return s.resetCheckProgress(sessionID)
		}
		return nil
	}
	kind, line := parseCheckAnswer(task)
	now := s.now()
	active := now.Add(idleCheckGrace).Unix()
	if kind == "" {
		// The next announce was not an answer. Consume it, keep the state.
		_, err = s.db.Exec(`UPDATE session_checks SET check_waiting=0 WHERE session_id=?`, sessionID)
		return err
	}
	name = protocol.CleanWaitingText(name)
	if name == "" {
		name = sessionID
	}
	switch kind {
	case "done":
		if _, err = s.db.Exec(`INSERT INTO session_checks(session_id,check_active,check_waiting,check_more,check_open,check_msgs)
			VALUES(?,?,0,0,0,0)
			ON CONFLICT(session_id) DO UPDATE SET check_active=excluded.check_active,check_waiting=0,
			check_more=0,check_open=0,check_msgs=0`, sessionID, active); err != nil {
			return err
		}
		if _, err = s.db.Exec(`UPDATE inbox_items SET status='closed',answer='done',answered_at=?
			WHERE session_id=? AND kind='check' AND status='open'`, now.UnixNano(), sessionID); err != nil {
			return err
		}
	case "more":
		a, ok, err := s.Agent(sessionID)
		if err != nil {
			return err
		}
		if ok {
			one := []protocol.AgentView{a}
			if err := s.applyPending(one); err != nil {
				return err
			}
			a = one[0]
		}
		workers := 0
		if ok {
			workers, err = s.owningWorkerCount(sessionID)
			if err != nil {
				return err
			}
		}
		open := checkOpenWork(a, workers)
		msgs, err := s.checkMessageCount(sessionID)
		if err != nil {
			return err
		}
		streak := st.more + 1
		if st.more > 0 && (open != st.open || msgs != st.msgs) {
			streak = 1
		}
		if _, err = s.db.Exec(`INSERT INTO session_checks(session_id,check_active,check_waiting,check_more,check_open,check_msgs)
			VALUES(?,?,0,?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET check_active=excluded.check_active,check_waiting=0,
			check_more=excluded.check_more,check_open=excluded.check_open,check_msgs=excluded.check_msgs`,
			sessionID, active, streak, open, msgs); err != nil {
			return err
		}
		text := name + " stopped with work left"
		if line != "" {
			text += ": " + line
		}
		if err = s.checkInboxItem(sessionID, node, text); err != nil {
			return err
		}
	case "blocked":
		if _, err = s.db.Exec(`INSERT INTO session_checks(session_id,check_active,check_waiting)
			VALUES(?,?,0)
			ON CONFLICT(session_id) DO UPDATE SET check_active=excluded.check_active,check_waiting=0`,
			sessionID, active); err != nil {
			return err
		}
		text := name + " is blocked"
		if line != "" {
			text += " on " + line
		}
		if err = s.checkInboxItem(sessionID, node, text); err != nil {
			return err
		}
	}
	return s.bumpCheckAnswer(kind)
}

// checkInboxItem leaves one open row per session, so more or a later block
// updates the same Needs you row instead of adding another.
func (s *Store) checkInboxItem(sessionID, node, text string) error {
	text = protocol.NeutralizeMarkers(text)
	var id string
	err := s.db.QueryRow(`SELECT id FROM inbox_items WHERE session_id=? AND kind='check' AND status='open'
		ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE inbox_items SET text=?,node=? WHERE id=?`, text, node, id)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.createInboxItem(s.db, protocol.InboxItem{Kind: "check", SessionID: sessionID, Node: node, Text: text,
		Recommendation: "Open the session and ask what is left, or message it."})
	return err
}

func (s *Store) bumpCheckAnswer(kind string) error {
	_, err := s.db.Exec(`INSERT INTO check_answer_counts(day,kind,count) VALUES(?,?,1)
		ON CONFLICT(day,kind) DO UPDATE SET count=count+1`, s.now().UTC().Format(idleCheckDay), kind)
	return err
}

// checkCounts adds the idle check figures to the operator counts.
func (s *Store) checkCounts(c *protocol.SessionCounts) error {
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender='agent-net' AND idem_key LIKE 'check:%'`).
		Scan(&c.ChecksSent); err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT kind,SUM(count) FROM check_answer_counts GROUP BY kind`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return err
		}
		switch kind {
		case "done":
			c.CheckAnswersDone = n
		case "more":
			c.CheckAnswersMore = n
		case "blocked":
			c.CheckAnswersBlocked = n
		}
	}
	return rows.Err()
}
