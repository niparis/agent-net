package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The idle check (decision 0036). agent-net asks an idle agent with open work
// whether it is done, at most once per idle period. Slice 4 adds autonomy: a
// node whose setting is wake gets a continue message instead of a Needs you
// row, within its own daily cap and no-progress guard.
const (
	idleCheckAfter    = 20 * time.Minute
	idleCheckGrace    = 2 * time.Second
	idleCheckDailyCap = 6
	idleCheckBody     = "[agent-net] check: is your task done? Answer with agent_announce: done, more (one line), or blocked on X."
	idleCheckDay      = "2006-01-02"
	wakeDailyCap      = 6
	wakeReplyPrefix   = "[agent-net] continue: you said you have work left ("
)

type checkState struct {
	announcedAt int64
	fingerprint string
	active      int64
	waiting     bool
	more        int
	open        int
	msgs        int
	day         string
	count       int
	// taskHash is a short hash of the task the last check was about, or of
	// the "blocked" answer to it. The task text itself is never stored. It is
	// empty after "done" or "more" (the no-progress streak governs those) and
	// when the session had no task.
	taskHash string
	// The wake guard rails: today's count, the no-progress streak and the
	// progress markers of the last wake.
	wakeDay    string
	wakeCount  int
	wakeStreak int
	wakeOpen   int
	wakeMsgs   int
}

func (s *Store) readCheckState(id string) (checkState, error) {
	var st checkState
	var waiting int
	err := s.db.QueryRow(`SELECT check_active,check_waiting,check_more,check_open,check_msgs,check_day,check_day_count,check_task_hash,check_announced_at,check_fingerprint,
		wake_day,wake_day_count,wake_streak,wake_open,wake_msgs FROM sessions WHERE id=?`, id).
		Scan(&st.active, &waiting, &st.more, &st.open, &st.msgs, &st.day, &st.count, &st.taskHash, &st.announcedAt, &st.fingerprint,
			&st.wakeDay, &st.wakeCount, &st.wakeStreak, &st.wakeOpen, &st.wakeMsgs)
	if errors.Is(err, sql.ErrNoRows) {
		return checkState{}, nil
	}
	st.waiting = waiting != 0
	return st, err
}

// checkOpenWork counts what an idle check treats as work left: requests it
// owes an answer, running workers it started, and a non-empty announced task.
// A task that reads as the answer "done" is not work: the session announces
// it after a check, and counting it re-arms the check on every announce.
func checkOpenWork(a protocol.AgentView, workers int) int {
	n := workers
	if task := strings.TrimSpace(a.Profile.Task); task != "" {
		if kind, _ := parseCheckAnswer(task); kind != "done" {
			n++
		}
	}
	for _, p := range a.Pending {
		if !p.Outgoing {
			n++
		}
	}
	return n
}

// checkTaskHash is the short fingerprint of a task text. An empty task has an
// empty hash, which never matches.
func checkTaskHash(task string) string {
	task = strings.TrimSpace(task)
	if task == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(task))
	return hex.EncodeToString(sum[:8])
}

// ownerBlockedSQL is true for a session that waits for its owner. A check asks
// a question the session cannot act on then, so none is sent and a pending one
// is retired. The statement takes the session id column as its text.
const ownerBlockedSQL = `EXISTS(SELECT 1 FROM sessions w WHERE w.id=%[1]s AND w.waiting<>'')`

func (s *Store) ownerBlocked(id string) (bool, error) {
	var blocked bool
	err := s.db.QueryRow(`SELECT `+fmt.Sprintf(ownerBlockedSQL, "?")+``, id).Scan(&blocked)
	return blocked, err
}

func (s *Store) checkMessageCount(id string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE (sender=? OR recipient=?) AND sender<>'agent-net'`, id, id).Scan(&n)
	return n, err
}

// agentNode is the graph node a session speaks for. It is the project the
// machine worked out from its folder, then any project the agent announced.
func agentNode(a protocol.AgentView) string { return projectOfSession(a) }

// agentAutonomy reads the node's effective autonomy rule. A missing or invalid
// stored value is off.
func (s *Store) agentAutonomy(a protocol.AgentView) (string, error) {
	node := agentNode(a)
	graph, err := s.Graph()
	if err != nil {
		return protocol.AutonomyOff, err
	}
	node = chartedProject(graph, node)
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return protocol.AutonomyOff, err
	}
	return deliveryValue(snap, "charter.autonomy_mode", node)
}

// agentWakeOnMessage reads the node's effective wake_on_message setting. The
// default is on. Only an explicit off turns it off.
func (s *Store) agentWakeOnMessage(a protocol.AgentView) bool {
	node := agentNode(a)
	graph, err := s.Graph()
	if err != nil {
		return true
	}
	node = chartedProject(graph, node)
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return true
	}
	e, err := snap.Resolve("charter.wake_on_message", settings.Where{Repo: node, Agent: a.AgentID})
	if err != nil {
		return true
	}
	var val string
	if json.Unmarshal(e.Value, &val) != nil {
		return true
	}
	return val != protocol.WakeOnMessageOff
}

func wakeBody(line string) string {
	return wakeReplyPrefix + protocol.NeutralizeMarkers(line) + "). Carry on, then answer the next check."
}

func (s *Store) ctoSessionID() string {
	var id string
	if err := s.db.QueryRow(`SELECT value FROM gateway_state WHERE key='cto_session'`).Scan(&id); err != nil {
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
	prompted, err := s.loopPrompted()
	if err != nil {
		return nil, err
	}
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
		// A running standing loop prompts this session on its clock already.
		if prompted[a.SessionID] {
			continue
		}
		if a.Waiting != nil || a.Limited(now) {
			continue
		}
		if blocked, err := s.ownerBlocked(a.SessionID); err != nil {
			return nil, err
		} else if blocked {
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
		// An announce answers for this state until the work changes.
		if st.fingerprint != "" && st.fingerprint == checkFingerprint(a, workers[a.SessionID]) {
			continue
		}
		// New work after an announce can re-arm the check without a turn.
		if a.LastActive <= st.active && (st.announcedAt == 0 || st.waiting) {
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
		// A session that was asked about this task today is not asked again
		// until the task text changes. Announcing the same task after the
		// answer is not new work. The daily cap below still applies.
		if hash := checkTaskHash(a.Profile.Task); st.fingerprint == "" && hash != "" && hash == st.taskHash && st.day == today {
			continue
		}
		if count >= idleCheckDailyCap {
			continue
		}
		if paused, err := s.nudgePaused(s.db, "idle_check", a.SessionID); err != nil {
			return nil, err
		} else if paused {
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

// loopPrompted is the set of sessions that a running standing loop prompts on
// its clock: the target loopTick picks for each node whose loop is running.
// The target is picked from every session, not only the online ones checkIdle
// loaded: loopTarget ranks an offline repo lead first, and loopTick may prompt
// that lead (waking it). Choosing from checkIdle's online list would name a
// different session. So this loads the full list again, and only while a loop
// is running.
func (s *Store) loopPrompted() (map[string]bool, error) {
	rows, err := s.loadLoops()
	if err != nil {
		return nil, err
	}
	var nodes []string
	for _, r := range rows {
		if r.every > 0 && r.paused == loopRunning {
			nodes = append(nodes, r.node)
		}
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, node := range nodes {
		if target, ok := loopTarget(agents, node); ok {
			out[target.SessionID] = true
		}
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
	if err = recordNudgeFire(tx, "idle_check", m.To, m.ID, m.CreatedAt); err != nil {
		return protocol.Message{}, err
	}
	if _, err = tx.Exec(`UPDATE sessions SET check_active=?,check_waiting=1,check_more=?,check_day=?,check_day_count=?,check_task_hash=? WHERE id=?`,
		now.Add(idleCheckGrace).Unix(), more, day, count, checkTaskHash(a.Profile.Task), a.SessionID); err != nil {
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
	_, err := s.db.Exec(`UPDATE sessions SET check_more=0,check_open=0,check_msgs=0 WHERE id=?`, sessionID)
	return err
}

// answerCheck acts on the next announce after a check. It runs for every
// announce; only a session with a check waiting is affected.
func (s *Store) answerCheck(sessionID, task string, reported *protocol.Waiting) error {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	var name, node string
	var st checkState
	var waiting int
	err := s.db.QueryRow(`SELECT check_active,check_waiting,check_more,check_open,check_msgs,check_day,check_day_count,
		wake_day,wake_day_count,wake_streak,wake_open,wake_msgs,name,node_id FROM sessions WHERE id=?`, sessionID).
		Scan(&st.active, &waiting, &st.more, &st.open, &st.msgs, &st.day, &st.count,
			&st.wakeDay, &st.wakeCount, &st.wakeStreak, &st.wakeOpen, &st.wakeMsgs, &name, &node)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if waiting == 0 {
		if st.more != 0 || st.open != 0 || st.msgs != 0 {
			return s.resetCheckProgress(sessionID)
		}
		return nil
	}
	kind, line := parseCheckAnswer(task)
	now := s.now()
	active := now.Add(idleCheckGrace).Unix()
	if kind == "" {
		_, err = s.db.Exec(`UPDATE sessions SET check_waiting=0 WHERE id=?`, sessionID)
		return err
	}
	name = protocol.CleanWaitingText(name)
	if name == "" {
		name = sessionID
	}
	switch kind {
	case "done":
		if _, err = s.db.Exec(`UPDATE sessions SET check_active=?,check_waiting=0,check_more=0,check_open=0,check_msgs=0,check_task_hash='' WHERE id=?`, active, sessionID); err != nil {
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
		autonomy := protocol.AutonomyOff
		online := false
		if ok {
			autonomy, err = s.agentAutonomy(a)
			if err != nil {
				return err
			}
			online = a.Online
		}
		today := now.UTC().Format(idleCheckDay)
		wakeDay, wakeCount, wakeStreak := st.wakeDay, st.wakeCount, st.wakeStreak
		if wakeDay != today {
			wakeCount = 0
		}
		wake, stopRow, fallbackRow := false, false, false
		paused, err := s.nudgePaused(s.db, "autonomy_continue", sessionID)
		if err != nil {
			return err
		}
		if autonomy == protocol.AutonomyWake && online && !paused {
			// Progress since the last wake resets the no-progress streak.
			if wakeStreak > 0 && (open != st.wakeOpen || msgs != st.wakeMsgs) {
				wakeStreak = 0
			}
			switch {
			case wakeStreak >= 2:
				// Two wakes with no progress stopped the waking. The one row
				// was left when the second wake went out.
			case wakeCount >= wakeDailyCap:
				fallbackRow = true
			default:
				wake = true
				wakeStreak++
				wakeCount++
				wakeDay = today
				stopRow = wakeStreak >= 2
			}
		} else if autonomy != protocol.AutonomyWake {
			fallbackRow = true
		}
		var m protocol.Message
		if wake {
			m = protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
				From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: sessionID,
				Type: protocol.TypeNotification, Body: wakeBody(line),
				IdempotencyKey: fmt.Sprintf("wake:%s:%s:%d", sessionID, today, wakeCount),
				CreatedAt:      now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if wake {
			if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
				VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
				m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
				return err
			}
			if err = recordNudgeFire(tx, "autonomy_continue", m.To, m.ID, m.CreatedAt); err != nil {
				return err
			}
			s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net wake")
		}
		wakeOpen, wakeMsgs := st.wakeOpen, st.wakeMsgs
		if wake {
			wakeOpen, wakeMsgs = open, msgs
		}
		// The task the session answered "more" about is remembered like a
		// "blocked" answer: the same line is not checked again today. A wake
		// session is the exception: its check makes the wake, so the
		// no-progress streak decides there, as before.
		moreHash := checkTaskHash(task)
		if autonomy == protocol.AutonomyWake {
			moreHash = ""
		}
		if _, err = tx.Exec(`UPDATE sessions SET check_active=?,check_waiting=0,check_more=?,check_open=?,check_msgs=?,check_task_hash=? WHERE id=?`,
			active, streak, open, msgs, moreHash, sessionID); err != nil {
			return err
		}
		if !paused && (wake || st.wakeDay != "") {
			if _, err = tx.Exec(`UPDATE sessions SET wake_day=?,wake_day_count=?,wake_streak=?,wake_open=?,wake_msgs=? WHERE id=?`,
				wakeDay, wakeCount, wakeStreak, wakeOpen, wakeMsgs, sessionID); err != nil {
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if wake {
			s.logMessage(m)
		}
		if stopRow {
			if err = s.checkInboxItem(sessionID, node, name+" was woken twice with no progress"); err != nil {
				return err
			}
		}
		if fallbackRow {
			text := name + " stopped with work left"
			if line != "" {
				text += ": " + line
			}
			if err = s.checkInboxItem(sessionID, node, text); err != nil {
				return err
			}
		}
	case "blocked":
		if _, err = s.db.Exec(`UPDATE sessions SET check_active=?,check_waiting=0,check_task_hash=? WHERE id=?`,
			active, checkTaskHash(task), sessionID); err != nil {
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
	return nil
}

// checkInboxItem logs an idle-check notice (decision 0196: no Desk row).
func (s *Store) checkInboxItem(sessionID, node, text string) error {
	s.logf("idle_check.notice", "session", sessionID, "node", node, "text", protocol.NeutralizeMarkers(text))
	return nil
}

// checkCounts adds the idle check figures to the operator counts.
func (s *Store) checkCounts(c *protocol.SessionCounts) error {
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE sender='agent-net' AND idem_key LIKE 'check:%'`).
		Scan(&c.ChecksSent); err != nil {
		return err
	}
	return s.db.QueryRow(`SELECT COALESCE(SUM(wake_day_count),0) FROM sessions WHERE wake_day=?`,
		s.now().UTC().Format(idleCheckDay)).Scan(&c.WakeUpsToday)
}

// checkFingerprint keeps task content out of durable check state. Only the
// owed request count matters; outgoing requests are somebody else's work.
func checkFingerprint(a protocol.AgentView, workers int) string {
	owed := 0
	for _, p := range a.Pending {
		if !p.Outgoing {
			owed++
		}
	}
	waitingState := protocol.CleanWaiting(a.Waiting)
	if waitingState != nil {
		copy := *waitingState
		copy.Since = 0
		waitingState = &copy
	}
	waiting, _ := json.Marshal(waitingState)
	return checkTaskHash(fmt.Sprintf("%s:%s:%d:%d", checkTaskHash(a.Profile.Task), waiting, owed, workers))
}

func (s *Store) recordCheckAnnounce(id string, waiting *protocol.Waiting) error {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	a, ok, err := s.Agent(id)
	if err != nil || !ok {
		return err
	}
	a.Waiting = protocol.CleanWaiting(waiting)
	agents := []protocol.AgentView{a}
	if err := s.applyPending(agents); err != nil {
		return err
	}
	workers, err := s.owningWorkerCount(id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sessions SET check_announced_at=?,check_fingerprint=? WHERE id=?`, s.now().UnixNano(), checkFingerprint(agents[0], workers), id)
	return err
}
