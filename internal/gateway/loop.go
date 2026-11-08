package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Standing loops (decision 0094). A node whose loop.every setting is set has
// its lead agent woken on a clock and asked to keep working toward the node's
// purpose. The expiry loop runs loopTick once a minute. One query finds every
// node with a loop; with no loop set the tick does nothing else.
//
// The safety stops reuse the autonomy wake rules: the day's cap is the same 6
// wakes of a session a day, counted in session_wakes with the idle-check
// wakes, and two ticks in a row with no progress pause the loop. Progress is a
// new announce, a new message, or a change in open work.

// loopRow is a node's loop: the setting and the stored state.
type loopRow struct {
	node        string
	every       time.Duration
	everyText   string
	prompt      string
	mode        string
	plan        string
	has         bool
	sentAt      int64
	startedAt   int64
	lastAt      int64
	sessionID   string
	day         string
	dayTicks    int
	dayProgress int
	streak      int
	lastOpen    int
	lastMsgs    int
	announced   bool
	awaiting    bool
	counted     bool
	paused      int
	lastResult  string
	ask         bool
}

// Why a loop is paused. A cap pause ends with the day; the other waits for
// the owner.
const (
	loopRunning  = 0
	loopNoMoving = 1
	loopCapped   = 2
)

const loopCols = `COALESCE(l.started_at,0),COALESCE(l.last_at,0),COALESCE(l.sent_at,0),COALESCE(l.session_id,''),COALESCE(l.day,''),
	COALESCE(l.day_ticks,0),COALESCE(l.day_progress,0),COALESCE(l.streak,0),COALESCE(l.last_open,0),COALESCE(l.last_msgs,0),
	COALESCE(l.announced,0),COALESCE(l.awaiting,0),COALESCE(l.counted,0),COALESCE(l.paused,0),COALESCE(l.last_result,'')`

func loopRef(node string) string { return "loop:" + node }

// loadLoops reads every node with a loop setting, and every stored loop whose
// setting is gone, in one statement. Both halves read primary keys.
// loopTables are the tables loadLoops reads. Keep it equal to the query there
// (see readThrough).
var loopTables = []string{"setting_values", "agent_loops"}

func (s *Store) loadLoops() ([]loopRow, error) {
	rows, err := s.db.Query(`SELECT v.scope,v.value,
		COALESCE((SELECT p.value FROM setting_values p WHERE p.key='loop.prompt' AND p.level='repo' AND p.scope=v.scope AND p.item=''),''),
		COALESCE((SELECT p.value FROM setting_values p WHERE p.key='loop.mode' AND p.level='repo' AND p.scope=v.scope AND p.item=''),'""'),
		COALESCE((SELECT p.value FROM setting_values p WHERE p.key='loop.plan' AND p.level='repo' AND p.scope=v.scope AND p.item=''),'""'),
		l.node IS NOT NULL,` + loopCols + `
		FROM setting_values v LEFT JOIN agent_loops l ON l.node=v.scope
		WHERE v.key='loop.every' AND v.level='repo' AND v.item=''
		UNION ALL
		SELECT l.node,'""','','""','""',1,` + loopCols + `
		FROM agent_loops l WHERE NOT EXISTS (SELECT 1 FROM setting_values v
		WHERE v.key='loop.every' AND v.level='repo' AND v.item='' AND v.scope=l.node)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []loopRow
	for rows.Next() {
		var r loopRow
		var every, prompt, mode, plan string
		var announced, awaiting, counted int
		if err := rows.Scan(&r.node, &every, &prompt, &mode, &plan, &r.has, &r.startedAt, &r.lastAt, &r.sentAt, &r.sessionID, &r.day,
			&r.dayTicks, &r.dayProgress, &r.streak, &r.lastOpen, &r.lastMsgs, &announced, &awaiting, &counted, &r.paused, &r.lastResult); err != nil {
			return nil, err
		}
		r.announced, r.awaiting, r.counted = announced != 0, awaiting != 0, counted != 0
		var text string
		if json.Unmarshal([]byte(every), &text) == nil {
			if d, err := time.ParseDuration(strings.TrimSpace(text)); err == nil && d > 0 {
				r.every, r.everyText = d, strings.TrimSpace(text)
			}
		}
		var p string
		if json.Unmarshal([]byte(prompt), &p) == nil {
			r.prompt = p
		}
		if json.Unmarshal([]byte(mode), &p) == nil && p == protocol.LoopModeOnChange {
			r.mode = p
		}
		if json.Unmarshal([]byte(plan), &p) == nil && protocol.ValidateNodeSetting(protocol.SettingLoopPlan, p) == nil {
			r.plan = p
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r loopRow) next() int64 {
	if r.every <= 0 {
		return 0
	}
	base := max(r.lastAt, r.startedAt)
	return base + int64(r.every/time.Second)
}

// view is the row as the Graph card and the CTO read it. Ticks and progress
// count the current day only.
func (r loopRow) view(today string) *protocol.LoopView {
	v := &protocol.LoopView{Every: r.everyText, Mode: r.mode, Plan: r.plan, LastTick: r.lastAt, NextTick: r.next(), Paused: r.paused != loopRunning, LastResult: r.lastResult}
	if r.day == today {
		v.TicksToday, v.ProgressToday = r.dayTicks, r.dayProgress
	}
	if v.Paused {
		v.NextTick = 0
	}
	return v
}

// loopViews returns the loop of each node that has one, by node name. It is
// one query and, with no loop set, one empty result.
func (s *Store) loopViews() (map[string]*protocol.LoopView, error) {
	rows, err := s.loadLoops()
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	today := s.now().UTC().Format(idleCheckDay)
	out := map[string]*protocol.LoopView{}
	for _, r := range rows {
		if r.every > 0 {
			out[r.node] = r.view(today)
		}
	}
	return out, nil
}

func (s *Store) applyLoops(agents []protocol.AgentView) error {
	views, err := s.loopViews()
	if err != nil || len(views) == 0 {
		return err
	}
	for i := range agents {
		if v, ok := views[nodeName(agents[i])]; ok && agents[i].Worker == nil {
			agents[i].Loop = v
		}
	}
	return nil
}

func (s *Store) applyLoopsToGraph(g *protocol.Graph) error {
	views, err := s.loopViews()
	if err != nil || len(views) == 0 {
		return err
	}
	for i := range g.Nodes {
		if v, ok := views[g.Nodes[i].Name]; ok {
			g.Nodes[i].Loop = v
		}
	}
	return nil
}

// loopTarget picks the session a tick goes to: the repo lead, else the most
// recent live session. Workers, owners and the CTO are never targets.
func loopTarget(agents []protocol.AgentView, node string) (protocol.AgentView, bool) {
	var c []protocol.AgentView
	for _, a := range agents {
		if a.Worker != nil || protocol.IsOwner(a.Harness) || a.Harness == "cto" || a.Harness == "bot" || nodeName(a) != node {
			continue
		}
		c = append(c, a)
	}
	if len(c) == 0 {
		return protocol.AgentView{}, false
	}
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].RepoLead != c[j].RepoLead {
			return c[i].RepoLead
		}
		if c[i].Online != c[j].Online {
			return c[i].Online
		}
		return c[i].LastActive > c[j].LastActive
	})
	return c[0], true
}

// loopTickResult is what the gateway must still do after a tick: dispatch the
// message, which wakes an offline session.
type loopTickResult struct {
	Node    string
	Outcome string
	Message *protocol.Message
}

// loopTick runs the clock once. It returns the messages to dispatch.
func (s *Store) loopTick() ([]loopTickResult, error) {
	rows, err := s.loadLoops()
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	now := s.now()
	today := now.UTC().Format(idleCheckDay)
	var due []loopRow
	for _, r := range rows {
		switch {
		case r.every <= 0:
			if err := s.loopRemove(r.node); err != nil {
				return nil, err
			}
		case !r.has:
			// The clock starts when the loop is first seen.
			if _, err := s.db.Exec(`INSERT OR IGNORE INTO agent_loops(node,started_at) VALUES(?,?)`, r.node, now.Unix()); err != nil {
				return nil, err
			}
		default:
			if r.paused == loopCapped && r.day != today {
				// The cap is a day's cap: a new day lifts it.
				if _, err := s.db.Exec(`UPDATE agent_loops SET paused=0 WHERE node=?`, r.node); err != nil {
					return nil, err
				}
				if err := s.closeAskRef(s.db, loopRef(r.node), protocol.AskReasonResolved); err != nil {
					return nil, err
				}
				r.paused = loopRunning
			}
			if r.paused == loopRunning && now.Unix() >= r.next() {
				due = append(due, r)
			}
		}
	}
	if len(due) == 0 {
		return nil, nil
	}
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		return nil, err
	}
	graph, err := s.Graph()
	if err != nil {
		return nil, err
	}
	var out []loopTickResult
	for _, r := range due {
		res, err := s.loopOne(r, agents, graph, now, today)
		if err != nil {
			return out, err
		}
		if res.Outcome != "" {
			out = append(out, res)
		}
	}
	return out, nil
}

// loopRemove drops a loop whose setting is gone and closes its ask.
func (s *Store) loopRemove(node string) error {
	if _, err := s.db.Exec(`DELETE FROM agent_loops WHERE node=?`, node); err != nil {
		return err
	}
	return s.closeAskRef(s.db, loopRef(node), protocol.AskReasonResolved)
}

func (s *Store) loopLog(node, text string) error {
	return s.deskLog(s.db, "gateway", "project", node, text, "", nil)
}

func (s *Store) loopOne(r loopRow, agents []protocol.AgentView, graph protocol.Graph, now time.Time, today string) (loopTickResult, error) {
	res := loopTickResult{Node: r.node}
	target, ok := loopTarget(agents, r.node)
	skip := func(outcome, why string) (loopTickResult, error) {
		if _, err := s.db.Exec(`UPDATE agent_loops SET last_at=? WHERE node=?`, now.Unix(), r.node); err != nil {
			return res, err
		}
		text := "Loop " + r.node + ": " + outcome
		if why != "" {
			text += " (" + why + ")"
		}
		res.Outcome = outcome
		return res, s.loopLog(r.node, text)
	}
	if !ok {
		return skip(protocol.LoopSkippedNone, "")
	}
	name := firstNonEmpty(target.Profile.Name, target.Title, target.SessionID)
	// Judge the last tick, if it has not been judged yet. A new announce
	// judged it already.
	streak, dayProgress, dayTicks := r.streak, r.dayProgress, r.dayTicks
	if r.day != today {
		// Yesterday's tick is judged below but not counted into today.
		dayProgress, dayTicks = 0, 0
	}
	if r.sessionID != "" && !r.counted {
		open, msgs, err := s.loopMarkers(r.sessionID)
		if err != nil {
			return res, err
		}
		if r.announced || open != r.lastOpen || msgs != r.lastMsgs {
			streak = 0
			if r.day == today {
				dayProgress++
			}
		} else {
			streak++
		}
		if _, err := s.db.Exec(`UPDATE agent_loops SET counted=1,streak=?,day_progress=?,day=? WHERE node=?`, streak, dayProgress, firstNonEmpty(r.day, today), r.node); err != nil {
			return res, err
		}
		r.counted = true
	}
	if streak >= protocol.LoopNoProgressStop {
		return s.loopPause(r, res, target, loopNoMoving, fmt.Sprintf("%d ticks in a row with no progress", streak), today)
	}
	// What changed since the last sent tick, from data agent-net already has.
	since := max(r.sentAt, r.startedAt)
	changes, err := s.loopChanges(target.SessionID, since)
	if err != nil {
		return res, err
	}
	if r.mode == protocol.LoopModeOnChange && changes.none() {
		return skip(protocol.LoopSkippedQuiet, "")
	}
	// Pi cannot be woken: its rpc mode does not open an existing session.
	if !target.Online && !wakeHarness(target.Harness) {
		return skip(protocol.LoopSkippedWake, target.Harness)
	}
	if target.Online {
		blocked, err := s.ownerBlocked(target.SessionID)
		if err != nil {
			return res, err
		}
		if target.Status != "idle" || target.Waiting != nil || target.Limited(now) || blocked {
			return skip(protocol.LoopSkippedBusy, "")
		}
	}
	// The cap is the wake rule's, per session, shared with the idle checks.
	var wakeDay string
	var wakeCount int
	err = s.db.QueryRow(`SELECT wake_day,wake_day_count FROM session_wakes WHERE session_id=?`, target.SessionID).Scan(&wakeDay, &wakeCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	if wakeDay != today {
		wakeCount = 0
	}
	if wakeCount >= wakeDailyCap {
		return s.loopPause(r, res, target, loopCapped, fmt.Sprintf("%d wakes today, the daily cap", wakeCount), today)
	}
	// Send.
	purpose := ""
	for _, n := range graph.Nodes {
		if n.Name == r.node {
			purpose = n.Charter.Purpose
			break
		}
	}
	open, msgs, err := s.loopMarkers(target.SessionID)
	if err != nil {
		return res, err
	}
	outcome := protocol.LoopSent
	if !target.Online {
		outcome = protocol.LoopWoke
	}
	m := protocol.Message{ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
		From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: target.SessionID,
		Type: protocol.TypeNotification, Body: protocol.LoopMessage(protocol.LoopPrompt{Changes: changes.line(r.sentAt > 0, since), Purpose: purpose, Custom: r.prompt, Plan: r.plan}),
		IdempotencyKey: fmt.Sprintf("loop:%s:%d", target.SessionID, now.Unix()),
		CreatedAt:      now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
		m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
		return res, err
	}
	s.event(tx, "msg.stored", "", m.From, m.ID, "type=notification by=agent-net loop")
	if _, err = tx.Exec(`INSERT INTO session_wakes(session_id,wake_day,wake_day_count) VALUES(?,?,1)
		ON CONFLICT(session_id) DO UPDATE SET wake_day=excluded.wake_day,
		wake_day_count=CASE WHEN session_wakes.wake_day=excluded.wake_day THEN session_wakes.wake_day_count+1 ELSE 1 END`,
		target.SessionID, today); err != nil {
		return res, err
	}
	if _, err = tx.Exec(`UPDATE agent_loops SET last_at=?,sent_at=?,session_id=?,day=?,day_ticks=?,day_progress=?,streak=?,last_open=?,last_msgs=?,
		announced=0,awaiting=1,counted=0 WHERE node=?`, now.Unix(), now.Unix(), target.SessionID, today, dayTicks+1, dayProgress, streak, open, msgs, r.node); err != nil {
		return res, err
	}
	if err = s.deskLog(tx, "gateway", "project", r.node, "Loop "+r.node+": "+outcome+" to "+name, "", nil); err != nil {
		return res, err
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	s.logMessage(m)
	res.Outcome, res.Message = outcome, &m
	return res, nil
}

// loopMarkers are the progress markers of the wake rule for one session: its
// open work and the messages it has sent or received from others.
func (s *Store) loopMarkers(session string) (open, msgs int, err error) {
	a, ok, err := s.Agent(session)
	if err != nil {
		return 0, 0, err
	}
	if ok {
		one := []protocol.AgentView{a}
		if err = s.applyPending(one); err != nil {
			return 0, 0, err
		}
		workers, err := s.owningWorkerCount(session)
		if err != nil {
			return 0, 0, err
		}
		open = checkOpenWork(one[0], workers)
	}
	msgs, err = s.checkMessageCount(session)
	return open, msgs, err
}

// loopPause stops the clock and opens the one Desk ask of the node.
func (s *Store) loopPause(r loopRow, res loopTickResult, target protocol.AgentView, reason int, why, today string) (loopTickResult, error) {
	ticks, progress := r.dayTicks, r.dayProgress
	if r.day != today {
		ticks, progress = 0, 0
	}
	if _, err := s.db.Exec(`UPDATE agent_loops SET paused=?,day=?,day_ticks=?,day_progress=?,last_at=? WHERE node=?`,
		reason, today, ticks, progress, s.now().Unix(), r.node); err != nil {
		return res, err
	}
	text := "Loop paused for " + r.node + ": " + why
	if _, err := s.createInboxItem(s.db, protocol.InboxItem{Kind: protocol.AskKindCheck, SessionID: target.SessionID, Node: r.node, Text: text,
		Recommendation: "Resume the loop, or stop it.", Options: []string{protocol.LoopAnswerResume, protocol.LoopAnswerStop},
		FromKind: protocol.AskFromGateway, SourceRef: loopRef(r.node), AboutKind: "project", AboutID: r.node}); err != nil {
		return res, err
	}
	res.Outcome = protocol.LoopPaused
	return res, s.loopLog(r.node, "Loop "+r.node+": "+protocol.LoopPaused+" ("+why+")")
}

// loopAnnounce attaches the next announce after a tick to its Desk log as the
// result line. It runs for every announce and costs one small read.
func (s *Store) loopAnnounce(session, task string) error {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	var node, day string
	var counted, progress, streak int
	err := s.db.QueryRow(`SELECT node,day,counted,day_progress,streak FROM agent_loops WHERE session_id=? AND awaiting=1`, session).
		Scan(&node, &day, &counted, &progress, &streak)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	line := askFirstLine(task, 160)
	if counted == 0 {
		counted = 1
		streak = 0
		if day == s.now().UTC().Format(idleCheckDay) {
			progress++
		}
	}
	if _, err := s.db.Exec(`UPDATE agent_loops SET announced=1,awaiting=0,counted=?,day_progress=?,streak=?,last_result=? WHERE node=?`,
		counted, progress, streak, line, node); err != nil {
		return err
	}
	if line == "" {
		return nil
	}
	return s.loopLog(node, "Loop "+node+" result: "+line)
}

// answerLoopAsk handles the owner's answer to a paused loop's ask. Resume
// restarts the clock at once and clears the streak. Stop clears the setting.
func (s *Store) answerLoopAsk(id, answer string, validate ...bool) (protocol.InboxItem, bool, error) {
	var node, ref string
	err := s.db.QueryRow(`SELECT node,source_ref FROM inbox_items WHERE id=? AND status='open' AND source_ref LIKE 'loop:%'`, id).Scan(&node, &ref)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.InboxItem{}, false, nil
	}
	if err != nil {
		return protocol.InboxItem{}, true, err
	}
	if answer != protocol.LoopAnswerResume && answer != protocol.LoopAnswerStop {
		return protocol.InboxItem{}, false, nil
	}
	item, err := scanInbox(s.db.QueryRow(`SELECT `+inboxColumns+` FROM inbox_items WHERE id=?`, id))
	if err != nil {
		return item, true, err
	}
	if len(validate) > 0 && validate[0] {
		return item, true, nil
	}
	switch answer {
	case protocol.LoopAnswerStop:
		if err := s.SetNodeSetting(node, protocol.SettingLoopEvery, "", "owner"); err != nil {
			return item, true, err
		}
		if err := s.loopRemove(node); err != nil {
			return item, true, err
		}
	default:
		s.checkMu.Lock()
		_, err := s.db.Exec(`UPDATE agent_loops SET paused=0,streak=0,announced=0,counted=1,last_at=0,started_at=0 WHERE node=?`, node)
		s.checkMu.Unlock()
		if err != nil {
			return item, true, err
		}
	}
	now := s.now().UTC()
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='answered',answer=?,answered_at=? WHERE id=?`, answer, now.UnixNano(), id); err != nil {
		return item, true, err
	}
	item.Status, item.Answer, item.AnsweredAt = "answered", answer, now
	return item, true, s.loopLog(node, "Loop "+node+": "+answer+" by the owner")
}

// loopTick runs the standing loops' clock and dispatches what it stored. A
// message to an offline session goes through the wake path, which resumes the
// same session and never starts a new one.
func (g *Gateway) loopTick(ctx context.Context) {
	results, err := g.store.loopTick()
	if err != nil {
		g.logLine("error", "where", "standing loops", "err", err)
	}
	for _, r := range results {
		if r.Message != nil {
			g.dispatch(ctx, *r.Message)
		}
	}
}

// loopChanges is what happened to one session since a time: messages to it,
// worker hand-backs to it, step tasks for it and its own Desk asks that opened
// or closed. Each is a list of ids. It is five small queries, run only for a
// due tick. The start is inclusive, so a change in the same second as the
// last tick is reported twice rather than missed.
type loopChanges struct {
	messages, handbacks, steps, asksNew, asksClosed []string
}

func (c loopChanges) none() bool {
	return len(c.messages)+len(c.handbacks)+len(c.steps)+len(c.asksNew)+len(c.asksClosed) == 0
}

const loopChangeIDs = 5

func (s *Store) loopIDs(query string, args ...any) ([]string, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) loopChanges(session string, sinceUnix int64) (loopChanges, error) {
	var c loopChanges
	nano := sinceUnix * int64(time.Second)
	var err error
	if c.messages, err = s.loopIDs(`SELECT id FROM messages WHERE recipient=? AND created_at>=? AND sender<>'agent-net' ORDER BY created_at`, session, nano); err != nil {
		return c, err
	}
	if c.steps, err = s.loopIDs(`SELECT id FROM messages WHERE recipient=? AND created_at>=? AND sender='agent-net' AND idem_key GLOB 'step_*' ORDER BY created_at`, session, nano); err != nil {
		return c, err
	}
	if c.handbacks, err = s.loopIDs(`SELECT session_id FROM worker_runs WHERE ended_at>=? AND caller_session_id=? ORDER BY ended_at`, sinceUnix, session); err != nil {
		return c, err
	}
	if c.asksNew, err = s.loopIDs(`SELECT id FROM inbox_items WHERE session_id=? AND created_at>=? AND source_ref NOT LIKE 'loop:%' ORDER BY created_at`, session, nano); err != nil {
		return c, err
	}
	c.asksClosed, err = s.loopIDs(`SELECT id FROM inbox_items WHERE session_id=? AND status<>'open' AND answered_at>=? AND source_ref NOT LIKE 'loop:%' ORDER BY answered_at`, session, nano)
	return c, err
}

// line is the prompt's opening: what changed, with counts and ids.
func (c loopChanges) line(afterTick bool, sinceUnix int64) string {
	at := time.Unix(sinceUnix, 0).UTC().Format("15:04") + " UTC"
	if c.none() {
		return "Nothing new since " + at
	}
	label := "Since the loop started (" + at + ")"
	if afterTick {
		label = "Since your last loop tick (" + at + ")"
	}
	part := func(ids []string, one, many string) string {
		if len(ids) == 0 {
			return ""
		}
		shown, more := ids, ""
		if len(shown) > loopChangeIDs {
			shown, more = shown[:loopChangeIDs], fmt.Sprintf(", +%d more", len(ids)-loopChangeIDs)
		}
		noun := many
		if len(ids) == 1 {
			noun = one
		}
		return fmt.Sprintf("%d %s (%s%s)", len(ids), noun, strings.Join(shown, ", "), more)
	}
	var parts []string
	for _, p := range []string{
		part(c.messages, "message", "messages"),
		part(c.handbacks, "worker hand-back", "worker hand-backs"),
		part(c.steps, "step task", "step tasks"),
		part(c.asksNew, "new Desk ask", "new Desk asks"),
		part(c.asksClosed, "closed Desk ask", "closed Desk asks"),
	} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return label + ": " + strings.Join(parts, ", ")
}
