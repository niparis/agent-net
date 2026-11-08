package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const loopEvery = 4 * time.Hour

// loopNode charts the project "proj" for the session, gives it a purpose and
// sets a 4h loop. The session is online and idle.
func loopNode(t *testing.T, s *Store, c *clock, session string) {
	t.Helper()
	setAutonomy(t, s, session, protocol.AutonomyOff)
	if _, err := s.db.Exec(`INSERT INTO graph_charter VALUES('proj','purpose','Ship the parser',?, 'owner')`, s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("proj", protocol.SettingLoopEvery, "4h", "owner"); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, session, time.Minute)
}

func loopMessages(t *testing.T, s *Store) []protocol.Message {
	t.Helper()
	rows, err := s.db.Query(`SELECT id,recipient,body,idem_key FROM messages WHERE sender='agent-net' AND idem_key LIKE 'loop:%' ORDER BY created_at,rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []protocol.Message
	for rows.Next() {
		var m protocol.Message
		if err := rows.Scan(&m.ID, &m.To, &m.Body, &m.IdempotencyKey); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func loopTicks(t *testing.T, s *Store) []loopTickResult {
	t.Helper()
	res, err := s.loopTick()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// runLoops advances the clock one interval and ticks.
func runLoops(t *testing.T, s *Store, c *clock) []loopTickResult {
	t.Helper()
	c.t = c.t.Add(loopEvery + time.Minute)
	return loopTicks(t, s)
}

func deskLines(t *testing.T, s *Store) []string {
	t.Helper()
	log, err := s.DeskLog(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(log) - 1; i >= 0; i-- {
		out = append(out, log[i].Text)
	}
	return out
}

func hasLine(lines []string, part string) bool {
	for _, l := range lines {
		if strings.Contains(l, part) {
			return true
		}
	}
	return false
}

func openLoopAsks(t *testing.T, s *Store) []protocol.Ask {
	t.Helper()
	asks, err := s.ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	var out []protocol.Ask
	for _, a := range asks {
		if strings.HasPrefix(a.Ref, "loop:") {
			out = append(out, a)
		}
	}
	return out
}

func TestLoopTickSendsThePromptToAnIdleSession(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	// The first run only starts the clock.
	if res := loopTicks(t, s); len(res) != 0 {
		t.Fatalf("first run = %+v", res)
	}
	c.t = c.t.Add(loopEvery - time.Minute)
	if res := loopTicks(t, s); len(res) != 0 {
		t.Fatalf("early run = %+v", res)
	}
	c.t = c.t.Add(2 * time.Minute)
	idleSince(t, s, c, b, time.Minute)
	res := loopTicks(t, s)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSent || res[0].Message == nil {
		t.Fatalf("tick = %+v", res)
	}
	m := loopMessages(t, s)
	want := "[agent-net] Loop: Nothing new since 12:00 UTC. Your purpose is Ship the parser. Check where you stand against it, do the next step, then announce what you did or what blocks you. " + protocol.LoopOwnerLine
	if len(m) != 1 || m[0].To != b || m[0].Body != want {
		t.Fatalf("messages = %+v, want body %q", m, want)
	}
	if !hasLine(deskLines(t, s), "Loop proj: sent to") {
		t.Fatalf("log = %v", deskLines(t, s))
	}
	// The same minute does not tick twice.
	if res := loopTicks(t, s); len(res) != 0 {
		t.Fatalf("second run = %+v", res)
	}
	// The Graph card and the session view carry the loop.
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	var loop *protocol.LoopView
	for _, n := range g.Nodes {
		if n.Name == "proj" {
			loop = n.Loop
		}
	}
	if loop == nil || loop.Every != "4h" || loop.TicksToday != 1 || loop.LastTick != c.t.Unix() || loop.NextTick != c.t.Add(loopEvery).Unix() {
		t.Fatalf("node loop = %+v", loop)
	}
	a, ok, err := s.Agent(b)
	if err != nil || !ok || a.Loop == nil || a.Loop.TicksToday != 1 {
		t.Fatalf("session loop = %+v %v %v", a.Loop, ok, err)
	}
}

func TestLoopPromptReplacesTheMiddleSentence(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	if err := s.SetNodeSetting("proj", protocol.SettingLoopPrompt, "Review the open Issues. Then Announce.", "owner"); err != nil {
		t.Fatal(err)
	}
	loopTicks(t, s)
	runLoops(t, s, c)
	m := loopMessages(t, s)
	if len(m) != 1 || m[0].Body != "[agent-net] Loop: Nothing new since 12:00 UTC. Your purpose is Ship the parser. Review the open Issues. Then Announce. "+protocol.LoopOwnerLine {
		t.Fatalf("messages = %+v", m)
	}
}

func TestLoopTickSkipsABusySession(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET status='busy' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	res := runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSkippedBusy || res[0].Message != nil {
		t.Fatalf("tick = %+v", res)
	}
	if m := loopMessages(t, s); len(m) != 0 {
		t.Fatalf("busy session got %+v", m)
	}
	if !hasLine(deskLines(t, s), "Loop proj: skipped busy") {
		t.Fatalf("log = %v", deskLines(t, s))
	}
	// A skip does not count as a tick, a wake or progress.
	g, _ := s.Graph()
	for _, n := range g.Nodes {
		if n.Name == "proj" && (n.Loop == nil || n.Loop.TicksToday != 0) {
			t.Fatalf("loop = %+v", n.Loop)
		}
	}
	if wakeUps(t, s) != 0 {
		t.Fatalf("a skipped tick counted as a wake")
	}
}

func wakeUps(t *testing.T, s *Store) int {
	t.Helper()
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return counts.WakeUpsToday
}

func TestLoopTwoTicksWithNoProgressPauseAndOpenOneAsk(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	for i := 1; i <= 2; i++ {
		idleSince(t, s, c, b, time.Minute)
		res := runLoops(t, s, c)
		if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
			t.Fatalf("tick %d = %+v", i, res)
		}
		idleSince(t, s, c, b, time.Minute)
	}
	idleSince(t, s, c, b, time.Minute)
	res := runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopPaused || res[0].Message != nil {
		t.Fatalf("third tick = %+v", res)
	}
	if m := loopMessages(t, s); len(m) != 2 {
		t.Fatalf("messages = %d, want 2", len(m))
	}
	asks := openLoopAsks(t, s)
	if len(asks) != 1 || strings.Join(asks[0].Options, ",") != "resume,stop" || !strings.Contains(asks[0].Question, "2 ticks in a row with no progress") {
		t.Fatalf("asks = %+v", asks)
	}
	// A paused loop stays quiet: no more ticks, no second ask.
	if res := runLoops(t, s, c); len(res) != 0 {
		t.Fatalf("paused loop ticked: %+v", res)
	}
	if asks := openLoopAsks(t, s); len(asks) != 1 {
		t.Fatalf("asks = %+v", asks)
	}
	g, _ := s.Graph()
	for _, n := range g.Nodes {
		if n.Name == "proj" && (n.Loop == nil || !n.Loop.Paused || n.Loop.NextTick != 0) {
			t.Fatalf("loop = %+v", n.Loop)
		}
	}
	// Resume clears the ask and the clock runs again at once.
	if _, err := s.AnswerInbox(asks[0].ID, protocol.LoopAnswerResume); err != nil {
		t.Fatal(err)
	}
	if asks := openLoopAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask left after resume: %+v", asks)
	}
	idleSince(t, s, c, b, time.Minute)
	res = loopTicks(t, s)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
		t.Fatalf("tick after resume = %+v", res)
	}
	// The answer did not message the session.
	for _, m := range loopMessages(t, s) {
		if m.Body == protocol.LoopAnswerResume {
			t.Fatalf("resume was sent to the session")
		}
	}
}

func TestLoopStopAnswerClearsTheSetting(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	for i := 0; i < 3; i++ {
		idleSince(t, s, c, b, time.Minute)
		runLoops(t, s, c)
	}
	asks := openLoopAsks(t, s)
	if len(asks) != 1 {
		t.Fatalf("asks = %+v", asks)
	}
	if _, err := s.AnswerInbox(asks[0].ID, protocol.LoopAnswerStop); err != nil {
		t.Fatal(err)
	}
	if asks := openLoopAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask left after stop: %+v", asks)
	}
	res, err := s.NodeSettings("proj")
	if err != nil || res.Overrides[protocol.SettingLoopEvery] != "" {
		t.Fatalf("settings = %+v %v", res, err)
	}
	if res := runLoops(t, s, c); len(res) != 0 {
		t.Fatalf("stopped loop ticked: %+v", res)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_loops`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("loop rows = %d %v", n, err)
	}
}

func TestLoopProgressKeepsTheLoopGoing(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	for i := 0; i < 5; i++ {
		idleSince(t, s, c, b, time.Minute)
		res := runLoops(t, s, c)
		if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
			t.Fatalf("tick %d = %+v", i, res)
		}
		// A new message between ticks is progress.
		if _, _, err := send(s, a, b, fmt.Sprintf("ping-%d", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	if asks := openLoopAsks(t, s); len(asks) != 0 {
		t.Fatalf("asks = %+v", asks)
	}
}

func TestLoopAnnounceIsProgressAndLogsTheResult(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	// Two ticks keep the test inside one UTC day.
	for i := 0; i < 2; i++ {
		idleSince(t, s, c, b, time.Minute)
		res := runLoops(t, s, c)
		if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
			t.Fatalf("tick %d = %+v", i, res)
		}
		if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "parser: wrote the lexer tests"}); err != nil {
			t.Fatal(err)
		}
	}
	lines := deskLines(t, s)
	if !hasLine(lines, "Loop proj result: parser: wrote the lexer tests") {
		t.Fatalf("log = %v", lines)
	}
	// The result follows its tick line.
	tick, result := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "Loop proj: sent to") && tick < 0 {
			tick = i
		}
		if strings.Contains(l, "Loop proj result:") && result < 0 {
			result = i
		}
	}
	if tick < 0 || result != tick+1 {
		t.Fatalf("tick at %d, result at %d: %v", tick, result, lines)
	}
	g, _ := s.Graph()
	for _, n := range g.Nodes {
		if n.Name == "proj" && (n.Loop == nil || n.Loop.ProgressToday != 2 || n.Loop.TicksToday != 2 || n.Loop.LastResult != "parser: wrote the lexer tests") {
			t.Fatalf("loop = %+v", n.Loop)
		}
	}
	if asks := openLoopAsks(t, s); len(asks) != 0 {
		t.Fatalf("an announcing loop paused: %+v", asks)
	}
}

func TestLoopSharesTheDailyCapWithTheWakeRule(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	// The idle-check wakes already used the day's six.
	today := s.now().Add(loopEvery + time.Minute).UTC().Format(idleCheckDay)
	if _, err := s.db.Exec(`INSERT INTO session_wakes(session_id,wake_day,wake_day_count) VALUES(?,?,?)`, b, today, wakeDailyCap); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, time.Minute)
	c.t = c.t.Add(loopEvery + time.Minute)
	idleSince(t, s, c, b, time.Minute)
	res := loopTicks(t, s)
	if len(res) != 1 || res[0].Outcome != protocol.LoopPaused {
		t.Fatalf("tick = %+v", res)
	}
	if m := loopMessages(t, s); len(m) != 0 {
		t.Fatalf("capped loop sent %+v", m)
	}
	asks := openLoopAsks(t, s)
	if len(asks) != 1 || !strings.Contains(asks[0].Question, "daily cap") {
		t.Fatalf("asks = %+v", asks)
	}
	// A new day lifts the cap and closes the ask.
	c.t = c.t.Add(24 * time.Hour)
	idleSince(t, s, c, b, time.Minute)
	res = loopTicks(t, s)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
		t.Fatalf("next day tick = %+v", res)
	}
	if asks := openLoopAsks(t, s); len(asks) != 0 {
		t.Fatalf("ask left: %+v", asks)
	}
}

func TestLoopTicksCountAsWakesForTheIdleCheckRule(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	idleSince(t, s, c, b, time.Minute)
	runLoops(t, s, c)
	if got := wakeUps(t, s); got != 1 {
		t.Fatalf("wake-ups today = %d, want 1", got)
	}
}

func TestLoopSkipsAnOfflinePiSession(t *testing.T) {
	s, c := newTestStore(t)
	node, _, err := s.CreateNode("hp", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "pi-native", Cwd: "/w/proj", Name: "pi"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOffline(node, id); err != nil {
		t.Fatal(err)
	}
	loopNode(t, s, c, id)
	loopTicks(t, s)
	res := runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSkippedWake || res[0].Message != nil {
		t.Fatalf("tick = %+v", res)
	}
	if !hasLine(deskLines(t, s), "skipped, cannot be woken (pi)") {
		t.Fatalf("log = %v", deskLines(t, s))
	}
}

func TestLoopWakesAnOfflineSessionThroughTheWakePath(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			p := newWakeProbe(t)
			c := &clock{t: p.s.now()}
			p.s.now = c.now
			// The autonomy rule is off: the loop is its own permission.
			id := p.offline(t, harness, protocol.ModeBackground, protocol.AutonomyOff)
			loopNode(t, p.s, c, id)
			if _, err := p.s.db.Exec(`UPDATE sessions SET status='idle' WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			p.g.loopTick(context.Background())
			p.expectNoWake(t)
			c.t = c.t.Add(loopEvery + time.Minute)
			p.g.loopTick(context.Background())
			w := p.expectWake(t, id)
			if w["autonomy"] != protocol.AutonomyWake {
				t.Fatalf("wake frame = %v", w)
			}
			if !hasLine(deskLines(t, p.s), "Loop proj: woke to") {
				t.Fatalf("log = %v", deskLines(t, p.s))
			}
			// The tick is a message to the same session, never a new one.
			if m := loopMessages(t, p.s); len(m) != 1 || m[0].To != id {
				t.Fatalf("messages = %+v", m)
			}
		})
	}
}

func TestLoopSettingIsValidatedAndKeepsItsCase(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyOff)
	_ = c
	for _, bad := range []string{"soon", "-1h", "0s"} {
		if err := s.SetNodeSetting("proj", protocol.SettingLoopEvery, bad, "owner"); err == nil {
			t.Fatalf("loop_every %q was accepted", bad)
		}
	}
	if err := s.SetNodeSetting("proj", protocol.SettingLoopPrompt, "Do The Next Step", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("proj", protocol.SettingLoopEvery, "90m", "cto"); err != nil {
		t.Fatal(err)
	}
	res, err := s.NodeSettings("proj")
	if err != nil || res.Overrides[protocol.SettingLoopPrompt] != "Do The Next Step" || res.Overrides[protocol.SettingLoopEvery] != "90m" {
		t.Fatalf("settings = %+v %v", res.Overrides, err)
	}
	// An empty value clears both.
	if err := s.SetNodeSetting("proj", protocol.SettingLoopEvery, "", "owner"); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.NodeSettings("proj"); res.Overrides[protocol.SettingLoopEvery] != "" {
		t.Fatalf("settings = %+v", res.Overrides)
	}
}

func TestNoLoopMeansNoWork(t *testing.T) {
	s, _ := newTestStore(t)
	if res := loopTicks(t, s); len(res) != 0 {
		t.Fatalf("tick = %+v", res)
	}
	rows, err := s.loadLoops()
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows = %+v %v", rows, err)
	}
}

// The CTO reads the loop in the public graph read and in the session view.
func TestLoopIsInTheGraphReadAndTheSessionView(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	idleSince(t, s, c, b, time.Minute)
	runLoops(t, s, c)
	g, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	page := protocol.PublicGraph(g, protocol.PublicGraphQuery{Node: "proj"})
	if len(page.Nodes) != 1 || page.Nodes[0].Loop == nil || page.Nodes[0].Loop.Every != "4h" || page.Nodes[0].Loop.TicksToday != 1 {
		t.Fatalf("graph read = %+v", page.Nodes)
	}
	a, ok, err := s.Agent(b)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	row := sessionRow(a, true, c.t, func(s string) string { return s })
	if row.Loop == nil || row.Loop.Every != "4h" || row.Loop.NextTick == 0 || row.Loop.LastTick == 0 {
		t.Fatalf("session row loop = %+v", row.Loop)
	}
	// A plain list row stays small.
	if row := sessionRow(a, false, c.t, func(s string) string { return s }); row.Loop != nil {
		t.Fatalf("list row carries the loop: %+v", row.Loop)
	}
}

// The CTO sets a loop without a proposal: it is advice, not a rule.
func TestCTOSetsALoopWithoutAProposal(t *testing.T) {
	s, _ := newTestStore(t)
	_, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyOff)
	class, err := s.CTOGraphClass("node_settings_set", []byte(`{"node":"proj","setting":"loop_every","value":"4h"}`))
	if err != nil || class != "major" {
		t.Fatalf("class = %q %v", class, err)
	}
	class, err = s.CTOGraphClass("node_settings_set", []byte(`{"node":"proj","setting":"autonomy","value":"wake"}`))
	if err != nil || class != "rule" {
		t.Fatalf("autonomy class = %q %v", class, err)
	}
}

func TestLoopPromptStartsWithWhatChanged(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	// Two messages to the session, one from agent-net itself (not counted),
	// a worker hand-back, a step task and an ask of its own.
	m1, _, err := send(s, a, b, "c1", "")
	if err != nil {
		t.Fatal(err)
	}
	m2, _, err := send(s, a, b, "c2", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO worker_runs(node_id,session_id,harness,outcome,ended_at,caller_session_id) VALUES('n','s_worker1','codex','done',?,?)`, s.now().Add(time.Minute).Unix(), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
		VALUES('m_step1','c_step','agent-net',?,'notification','task',0,'step_1_x',?,?,'stored')`, b, s.now().Add(time.Minute).UnixNano(), s.now().Add(time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	ask, err := s.createInboxItem(s.db, protocol.InboxItem{Kind: "rejection", SessionID: b, Text: "request refused"})
	if err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, time.Minute)
	runLoops(t, s, c)
	m := loopMessages(t, s)
	if len(m) != 1 {
		t.Fatalf("messages = %+v", m)
	}
	for _, want := range []string{
		"[agent-net] Loop: Since the loop started (12:00 UTC): 2 messages (" + m1.ID + ", " + m2.ID + "), 1 worker hand-back (s_worker1), 1 step task (m_step1), 1 new Desk ask (" + ask.ID + "). Your purpose is Ship the parser.",
	} {
		if !strings.HasPrefix(m[0].Body, want) {
			t.Fatalf("body = %q\nwant prefix %q", m[0].Body, want)
		}
	}
	// The next tick looks back only to the previous one, and says "last loop tick".
	idleSince(t, s, c, b, time.Minute)
	runLoops(t, s, c)
	m = loopMessages(t, s)
	if len(m) != 2 || !strings.HasPrefix(m[1].Body, "[agent-net] Loop: Nothing new since 16:01 UTC. Your purpose") {
		t.Fatalf("second body = %+v", m)
	}
}

func TestLoopOnChangeSkipsAQuietTick(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	if err := s.SetNodeSetting("proj", protocol.SettingLoopMode, "on_change", "owner"); err != nil {
		t.Fatal(err)
	}
	loopTicks(t, s)
	idleSince(t, s, c, b, time.Minute)
	res := runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSkippedQuiet || res[0].Message != nil {
		t.Fatalf("quiet tick = %+v", res)
	}
	if m := loopMessages(t, s); len(m) != 0 {
		t.Fatalf("sent %+v", m)
	}
	if !hasLine(deskLines(t, s), "Loop proj: skipped, nothing new") {
		t.Fatalf("log = %v", deskLines(t, s))
	}
	if wakeUps(t, s) != 0 {
		t.Fatal("a quiet tick counted as a wake")
	}
	// A skip never pauses the loop, however many there are.
	for i := 0; i < 3; i++ {
		idleSince(t, s, c, b, time.Minute)
		if res := runLoops(t, s, c); len(res) != 1 || res[0].Outcome != protocol.LoopSkippedQuiet {
			t.Fatalf("quiet tick %d = %+v", i, res)
		}
	}
	if asks := openLoopAsks(t, s); len(asks) != 0 {
		t.Fatalf("asks = %+v", asks)
	}
	// A new message makes the next tick go.
	if _, _, err := send(s, a, b, "news", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, time.Minute)
	res = runLoops(t, s, c)
	if len(res) != 1 || res[0].Outcome != protocol.LoopSent {
		t.Fatalf("tick after news = %+v", res)
	}
	m := loopMessages(t, s)
	if len(m) != 1 || !strings.Contains(m[0].Body, "1 message (") {
		t.Fatalf("messages = %+v", m)
	}
	// And with nothing new again, it skips again.
	idleSince(t, s, c, b, time.Minute)
	if res := runLoops(t, s, c); len(res) != 1 || res[0].Outcome != protocol.LoopSkippedQuiet {
		t.Fatalf("quiet again = %+v", res)
	}
	g, _ := s.Graph()
	for _, n := range g.Nodes {
		if n.Name == "proj" && (n.Loop == nil || n.Loop.Mode != "on_change") {
			t.Fatalf("loop = %+v", n.Loop)
		}
	}
}

func TestLoopPlanFileIsNamedInThePrompt(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	for _, bad := range []string{"/etc/passwd", "../x.md", "~/PLAN.md", "a\nb"} {
		if err := s.SetNodeSetting("proj", protocol.SettingLoopPlan, bad, "owner"); err == nil {
			t.Fatalf("plan %q was accepted", bad)
		}
	}
	if err := s.SetNodeSetting("proj", protocol.SettingLoopPlan, "docs/Plan.md", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("proj", protocol.SettingLoopMode, "sometimes", "owner"); err == nil {
		t.Fatal("bad loop_mode accepted")
	}
	loopTicks(t, s)
	runLoops(t, s, c)
	m := loopMessages(t, s)
	if len(m) != 1 || !strings.Contains(m[0].Body, " Read docs/Plan.md, do the next step, then update docs/Plan.md. "+protocol.LoopOwnerLine) {
		t.Fatalf("messages = %+v", m)
	}
}
