package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const checkBody = "[agent-net] check: is your task done? Answer with agent_announce: done, more (one line), or blocked on X."

// idleSince makes a session idle with its last activity m ago.
func idleSince(t *testing.T, s *Store, c *clock, id string, m time.Duration) {
	t.Helper()
	if _, err := s.db.Exec("UPDATE sessions SET status='idle',last_active=? WHERE id=?", c.t.Add(-m).UnixNano(), id); err != nil {
		t.Fatal(err)
	}
}

// activity makes a session report a turn at the current test time.
func activity(t *testing.T, s *Store, id, harness, native string) {
	t.Helper()
	node, err := s.SessionNode(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{SessionID: id, Harness: harness, NativeID: native,
		Name: "session", Status: "idle", LastActive: s.now().Unix()}); err != nil {
		t.Fatal(err)
	}
}

func checks(t *testing.T, s *Store) []protocol.Message {
	t.Helper()
	ms, err := s.checkIdle()
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestCheckFiresOnceAfterIdleWithOpenWork(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	got := checks(t, s)
	if len(got) != 1 {
		t.Fatalf("checks = %+v, want 1", got)
	}
	m := got[0]
	if m.From != "agent-net" || m.FromName != "agent-net" || m.FromHarness != "agent-net" ||
		m.To != b || m.Type != protocol.TypeNotification || m.Body != checkBody {
		t.Fatalf("check = %+v", m)
	}
	if again := checks(t, s); len(again) != 0 {
		t.Fatalf("second check in the same idle period: %+v", again)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestCheckNeedsOpenWork(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check without open work: %+v", got)
	}
}

func TestCheckActivityResetsTheIdlePeriod(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after activity = %+v", got)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 2 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestCheckAnswerDone(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "done"}); err != nil {
		t.Fatal(err)
	}
	if c.t = c.t.Add(21 * time.Minute); len(checks(t, s)) != 0 {
		t.Fatal("done did not stop checks until new activity")
	}
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after new activity = %+v", got)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.CheckAnswersDone != 1 || counts.ChecksSent != 2 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestCheckAnswerMoreLeavesOneNeedsYouRow(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: write the docs"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	var open []protocol.InboxItem
	for _, item := range items {
		if item.Kind == "check" && item.Status == "open" {
			open = append(open, item)
		}
	}
	if len(open) != 1 || !strings.Contains(open[0].Text, "stopped with work left: write the docs") {
		t.Fatalf("Needs you rows = %+v", open)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.CheckAnswersMore != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestCheckAnswerBlockedNamesTheBlocker(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "blocked on the deploy key"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Kind == "check" && item.Status == "open" && strings.Contains(item.Text, "the deploy key") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no blocked row: %+v", items)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.CheckAnswersBlocked != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// After a "more" answer the same task line is not checked again today, with
// a turn or with progress. A new task line is checked.
func TestCheckMoreWaitsForANewTaskLine(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := send(s, a, b, "req", "")
	if err != nil {
		t.Fatal(err)
	}
	node := mustNode(t, s, b)
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check 1 = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "more: first step"}); err != nil {
		t.Fatal(err)
	}
	// A turn changes no message and no open work.
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check after the same more line: %+v", got)
	}
	// Progress does not change the line either.
	if _, _, err := send(s, b, a, "reply", req.ID); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check after progress with the same more line: %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "working on the second step"}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after the task changed = %+v", got)
	}
}

func TestCheckSkipsWorkersCTOAndOfflineSessions(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	node := mustNode(t, s, a)
	// A running worker owned by a.
	w, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "worker-1", Name: "worker",
		Status: "idle", LastActive: c.t.Add(-21 * time.Minute).Unix(),
		Worker: &protocol.WorkerInfo{ID: "w1", Owner: a, StartedAt: c.t.Add(-time.Hour).Unix()}})
	if err != nil {
		t.Fatal(err)
	}
	// The CTO.
	cto, _, err := s.upsertSession(s.db, node, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO",
		Status: "idle", LastActive: c.t.Add(-21 * time.Minute).Unix()}, true)
	if err != nil {
		t.Fatal(err)
	}
	// An offline session with work.
	idleSince(t, s, c, b, 21*time.Minute)
	if _, err := s.db.Exec("UPDATE sessions SET online=0 WHERE id=?", b); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{w, cto, b} {
		if _, err := s.Announce(mustNode(t, s, id), protocol.ProfileAnnounce{SessionID: id, Task: "open work"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE sessions SET last_active=? WHERE id=?", c.t.Add(-21*time.Minute).UnixNano(), id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("checked a worker, the CTO or an offline session: %+v", got)
	}
}

func TestCheckDailyCap(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		idleSince(t, s, c, b, 21*time.Minute)
		if got := checks(t, s); len(got) != 1 {
			t.Fatalf("check %d = %+v", i+1, got)
		}
		c.t = c.t.Add(time.Minute)
		activity(t, s, b, "pi", "nb")
		c.t = c.t.Add(21 * time.Minute)
	}
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check %d over the daily cap: %+v", 7, got)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 6 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// waitOwner puts a session in the waiting-for-its-owner state.
func waitOwner(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sessions SET waiting=? WHERE id=?`,
		`{"kind":"question","text":"which branch?","since":1}`, id); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSkipsSessionWaitingOnItsOwner(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	waitOwner(t, s, b)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check for a waiting session: %+v", got)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET waiting='' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after the wait ended = %+v", got)
	}
}

func TestCheckSkipsSessionWithOpenOwnerQuestion(t *testing.T) {
	for _, kind := range []string{"question", "approval"} {
		s, c := newTestStore(t)
		a, b := twoSessions(t, s)
		if _, _, err := send(s, a, b, "req", ""); err != nil {
			t.Fatal(err)
		}
		idleSince(t, s, c, b, 21*time.Minute)
		item, err := s.CreateInboxItem(protocol.InboxItem{Kind: kind, SessionID: b, Text: "deploy?"})
		if err != nil {
			t.Fatal(err)
		}
		if got := checks(t, s); len(got) != 0 {
			t.Fatalf("%s: check with an open owner item: %+v", kind, got)
		}
		if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed' WHERE id=?`, item.ID); err != nil {
			t.Fatal(err)
		}
		if got := checks(t, s); len(got) != 1 {
			t.Fatalf("%s: check after the item closed = %+v", kind, got)
		}
	}
}

func TestCheckRetiresPendingCheckOfWaitingSession(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	got := checks(t, s)
	if len(got) != 1 {
		t.Fatalf("check = %+v", got)
	}
	waitOwner(t, s, b)
	if err := s.retireObsoleteNotices(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.db.QueryRow(`SELECT state FROM messages WHERE id=?`, got[0].ID).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("pending check state = %q, %v", state, err)
	}
}

// The live bug: a session answered "blocked on X", announced the same task
// again after every check, and every announce counted as new activity.
func TestCheckNotRepeatedAfterSameAnswerSameTask(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	task := "blocked on Nicolas: cleanup batch"
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: task}); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	for i := 0; i < 5; i++ {
		c.t = c.t.Add(time.Minute)
		if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: task}); err != nil {
			t.Fatal(err)
		}
		activity(t, s, b, "pi", "nb")
		c.t = c.t.Add(21 * time.Minute)
		if got := checks(t, s); len(got) != 0 {
			t.Fatalf("round %d: repeated check after the same answer: %+v", i, got)
		}
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// The live bug (2026-11-08): a session with open work was checked, answered
// "done", and announced "done" as its task. The task text counted as open
// work, so each new activity re-armed the check, up to the daily cap.
func TestCheckNotRepeatedAfterDone(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	node := mustNode(t, s, b)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "writing the migration"}); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "done"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		activity(t, s, b, "pi", "nb")
		c.t = c.t.Add(21 * time.Minute)
		if got := checks(t, s); len(got) != 0 {
			t.Fatalf("round %d: check after done: %+v", i, got)
		}
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 1 || counts.CheckAnswersDone != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

func TestCheckTaskChangeAllowsNewCheck(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	node := mustNode(t, s, b)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "blocked on Nicolas: cleanup"}); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "blocked on Nicolas: cleanup"}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check with the same task: %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "writing the migration"}); err != nil {
		t.Fatal(err)
	}
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after the task changed = %+v", got)
	}
}

// New conversations per check must not give a new allowance: the cap counts
// per session and day, and it holds when the task changes every time.
func TestCheckDailyCapHoldsWhenTaskChanges(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	node := mustNode(t, s, b)
	for i := 0; i < idleCheckDailyCap; i++ {
		if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: fmt.Sprintf("step %d", i)}); err != nil {
			t.Fatal(err)
		}
		activity(t, s, b, "pi", "nb")
		c.t = c.t.Add(21 * time.Minute)
		if got := checks(t, s); len(got) != 1 {
			t.Fatalf("check %d = %+v", i+1, got)
		}
		c.t = c.t.Add(time.Minute)
	}
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "step last"}); err != nil {
		t.Fatal(err)
	}
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check over the daily cap: %+v", got)
	}
}

// The live bug (2026-11-09): a lead answered "more" with a task line and kept
// that line. The answer cleared the stored task hash, so each new message and
// turn re-armed the check, about six a night.
func TestCheckNotRepeatedAfterMoreWithSameTask(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: write the docs"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		// A new message changes the progress markers, and a turn is activity.
		c.t = c.t.Add(time.Minute)
		if _, _, err := send(s, a, b, fmt.Sprintf("note-%d", i), ""); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(time.Minute)
		activity(t, s, b, "pi", "nb")
		c.t = c.t.Add(21 * time.Minute)
		if got := checks(t, s); len(got) != 0 {
			t.Fatalf("round %d: check after more with the same task: %+v", i, got)
		}
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 1 || counts.CheckAnswersMore != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// A running standing loop already prompts its target on its clock, so the
// idle check does not ask that session too. A paused loop prompts nothing,
// and the check runs again.
func TestCheckSkipsTheSessionAStandingLoopPrompts(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	if _, _, err := send(s, a, b, "req", ""); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check while the loop prompts the session: %+v", got)
	}
	if _, err := s.db.Exec(`UPDATE agent_loops SET paused=? WHERE node='proj'`, loopNoMoving); err != nil {
		t.Fatal(err)
	}
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check while the loop is paused = %+v, want 1", got)
	}
}

// A wake-mode session is not held by the same-line rule after "more": its
// check sends the wake, so the no-progress streak decides, as before. Progress
// and the same line still get it the next check.
func TestCheckWakeSessionAfterMoreWithProgressGetsItsNextCheck(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	setAutonomy(t, s, b, protocol.AutonomyWake)
	req, _, err := send(s, a, b, "req", "")
	if err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check 1 = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: first step"}); err != nil {
		t.Fatal(err)
	}
	if got := wakeMessages(t, s); len(got) != 1 {
		t.Fatalf("wake after more = %d, want 1", len(got))
	}
	// Progress: the request is answered. The task line is unchanged.
	c.t = c.t.Add(time.Minute)
	if _, _, err := send(s, b, a, "reply", req.ID); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("wake session check after more with progress = %+v, want 1", got)
	}
}
