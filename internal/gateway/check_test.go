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
	c.advance(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
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
	c.advance(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "done"}); err != nil {
		t.Fatal(err)
	}
	if c.advance(21 * time.Minute); len(checks(t, s)) != 0 {
		t.Fatal("done did not stop checks until new activity")
	}
	if _, _, err := send(s, a, b, "new work after done", ""); err != nil {
		t.Fatal(err)
	}
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after new activity = %+v", got)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 2 {
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
	c.advance(time.Minute)
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
	c.advance(time.Minute)
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
	c.advance(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "more: first step"}); err != nil {
		t.Fatal(err)
	}
	// A turn changes no message and no open work.
	c.advance(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check after the same more line: %+v", got)
	}
	// Answering an owed request changes the fingerprint even with the same line.
	if _, _, err := send(s, b, a, "reply", req.ID); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after progress with the same more line: %+v", got)
	}
	c.advance(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "working on the second step"}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("fresh announce after the task changed = %+v", got)
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
		c.advance(time.Minute)
		activity(t, s, b, "pi", "nb")
		c.advance(21 * time.Minute)
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
	task := "blocked on the owner: cleanup batch"
	seedCheckTask(t, s, b, task)
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	for i := 0; i < 5; i++ {
		c.advance(time.Minute)
		if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: task}); err != nil {
			t.Fatal(err)
		}
		activity(t, s, b, "pi", "nb")
		c.advance(21 * time.Minute)
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
	seedCheckTask(t, s, b, "writing the migration")
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	c.advance(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "done"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		c.advance(time.Minute)
		activity(t, s, b, "pi", "nb")
		c.advance(21 * time.Minute)
		if got := checks(t, s); len(got) != 0 {
			t.Fatalf("round %d: check after done: %+v", i, got)
		}
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 1 {
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
	seedCheckTask(t, s, b, "blocked on the owner: cleanup")
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("first check = %+v", got)
	}
	c.advance(time.Minute)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: b, Task: "blocked on the owner: cleanup"}); err != nil {
		t.Fatal(err)
	}
	c.advance(21 * time.Minute)
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check with the same task: %+v", got)
	}
	c.advance(time.Minute)
	seedCheckTask(t, s, b, "writing the migration")
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
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
	for i := 0; i < idleCheckDailyCap; i++ {
		seedCheckTask(t, s, b, fmt.Sprintf("step %d", i))
		activity(t, s, b, "pi", "nb")
		c.advance(21 * time.Minute)
		if got := checks(t, s); len(got) != 1 {
			t.Fatalf("check %d = %+v", i+1, got)
		}
		c.advance(time.Minute)
	}
	seedCheckTask(t, s, b, "step last")
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
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
	c.advance(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: write the docs"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		// A notification and a turn do not change the work fingerprint.
		c.advance(time.Minute)
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: fmt.Sprintf("note-%d", i), Type: protocol.TypeNotification}); err != nil {
			t.Fatal(err)
		}
		c.advance(time.Minute)
		activity(t, s, b, "pi", "nb")
		c.advance(21 * time.Minute)
		if got := checks(t, s); len(got) != 0 {
			t.Fatalf("round %d: check after more with the same task: %+v", i, got)
		}
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.ChecksSent != 1 {
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
	c.advance(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: first step"}); err != nil {
		t.Fatal(err)
	}
	if got := wakeMessages(t, s); len(got) != 1 {
		t.Fatalf("wake after more = %d, want 1", len(got))
	}
	// Progress: the request is answered. The task line is unchanged.
	c.advance(time.Minute)
	if _, _, err := send(s, b, a, "reply", req.ID); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("wake session check after more with progress = %+v, want 1", got)
	}
}

func TestCheckAnnounceFingerprint(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	announce(t, s, b, protocol.ProfileAnnounce{Task: "implement the fix"})
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("unchanged announce checked: %+v", got)
	}
	c.advance(25 * time.Hour)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("unchanged announce expired: %+v", got)
	}
	if _, _, err := send(s, a, b, "new request", ""); err != nil {
		t.Fatal(err)
	}
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("changed owed count: %+v", got)
	}
	announce(t, s, b, protocol.ProfileAnnounce{Task: "implement the fix"})
	c.advance(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("fresh announce checked again: %+v", got)
	}
	if _, _, err := send(s, a, b, "another request", ""); err != nil {
		t.Fatal(err)
	}
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("second owed count change: %+v", got)
	}
}

func TestCheckAnnounceClearsPreviousWaitingFingerprint(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET waiting='{"kind":"question","text":"Continue?","since":1}' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	announce(t, s, b, protocol.ProfileAnnounce{Task: "continuing the fix"})
	// The daemon reports the cleared wait after profile.announce returns.
	if _, err := s.db.Exec(`UPDATE sessions SET waiting='' WHERE id=?`, b); err != nil {
		t.Fatal(err)
	}
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("announce recorded the old waiting state: %+v", got)
	}
}

// seedCheckTask models a stored task without a fresh announce, including a
// task retained across the fingerprint migration.
func seedCheckTask(t *testing.T, s *Store, id, task string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sessions SET task=?,announced_at=? WHERE id=?`, task, s.now().UnixNano(), id); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAnnounceWorkerEndedChangesFingerprint(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	node := mustNode(t, s, b)
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "owned-worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: b}})
	if err != nil {
		t.Fatal(err)
	}
	announce(t, s, b, protocol.ProfileAnnounce{Task: "finish the worker's change"})
	idleSince(t, s, c, b, 21*time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("fresh announce checked: %+v", got)
	}
	if err := s.SetWorkerSessionEnded(node, worker); err != nil {
		t.Fatal(err)
	}
	if got := checks(t, s); len(got) != 1 || got[0].To != b {
		t.Fatalf("worker ending did not re-arm check: %+v", got)
	}
}

// The hourly check's "blocked on ..." answer repeats the wait the session
// already has on the Desk. When the chief already closed that wait as the
// same question, the answer must not make another check row.
func TestBlockedAnswerRepeatingASettledWaitMakesNoCheckRow(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	const wait = "owner's word on the TRUNCATE reset, then the lead's order to open the pin PR and deploy"
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: wait}})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	// The chief closed the wait as the same question as the open Desk ask.
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed', answer='chief: same wait' WHERE source_ref=?`, "waiting:"+sid); err != nil {
		t.Fatal(err)
	}
	// An hour later the check asks, and the session answers blocked.
	if _, err := s.db.Exec(`UPDATE sessions SET check_active=?,check_waiting=1 WHERE id=?`, c.t.Unix(), sid); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Hour)
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid, Task: "blocked on the owner's TRUNCATE decision"}); err != nil {
		t.Fatal(err)
	}
	if rows := openCheckRows(t, s); len(rows) != 0 {
		t.Fatalf("check rows after the same wait: %+v", rows)
	}
	var open, all int
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(status='open'),0), COUNT(*) FROM inbox_items WHERE source_ref=?`, "waiting:"+sid).Scan(&open, &all); err != nil {
		t.Fatal(err)
	}
	if open != 0 || all != 1 {
		t.Fatalf("waiting rows = %d open of %d, want the closed one only", open, all)
	}
}

// A wait whose text really changes is a new wait and raises a new ask.
func TestChangedWaitAfterAClosedAskRaisesANewAsk(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Delete worktree w_1?"}})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed', answer='chief: done' WHERE source_ref=?`, "waiting:"+sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: "Delete worktree w_2?"}}); err != nil {
		t.Fatal(err)
	}
	c.advance(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	var open, all int
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(status='open'),0), COUNT(*) FROM inbox_items WHERE source_ref=?`, "waiting:"+sid).Scan(&open, &all); err != nil {
		t.Fatal(err)
	}
	if open != 1 || all != 2 {
		t.Fatalf("waiting rows = %d open of %d, want a new ask", open, all)
	}
}

// A session goes busy to answer the check, and busy clears its stored wait
// (decision 0016). Its answer then repeats the wait in the announce itself.
// The chief already closed that wait, so neither a second waiting row nor a
// check row is raised.
func TestBlockedAnswerRepeatingItsOwnWaitAfterBusyMakesNoAsk(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	const wait = "owner's yes on the TRUNCATE, relayed by the lead, then pg_dump, truncate, merge and deploy"
	sid, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p",
		Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: wait}})
	if err != nil {
		t.Fatal(err)
	}
	since := c.t.Unix()
	c.advance(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	// The chief closed the wait as the same question.
	if _, err := s.db.Exec(`UPDATE inbox_items SET status='closed', answer='chief: same wait' WHERE source_ref=?`, "waiting:"+sid); err != nil {
		t.Fatal(err)
	}
	// An hour later the check arrives and the session goes busy to answer it.
	c.advance(time.Hour)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Status: "busy"}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT waiting FROM sessions WHERE id=?`, sid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Fatalf("the busy turn kept the wait: %s", raw)
	}
	// The check is out and waits for the answer.
	if _, err := s.db.Exec(`UPDATE sessions SET check_active=?,check_waiting=1 WHERE id=?`, c.t.Unix(), sid); err != nil {
		t.Fatal(err)
	}
	// The answer names the same wait again. The daemon kept its original start
	// across the busy turn.
	reported := &protocol.Waiting{Kind: protocol.WaitingInput, Text: wait, Since: since}
	if _, err := s.Announce(node, protocol.ProfileAnnounce{SessionID: sid,
		Task: "blocked on the owner's TRUNCATE reset", Waiting: reported}); err != nil {
		t.Fatal(err)
	}
	if rows := openCheckRows(t, s); len(rows) != 0 {
		t.Fatalf("check rows after the same wait: %+v", rows)
	}
	// The daemon reports the wait again, as it does after the announce.
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Waiting: reported}); err != nil {
		t.Fatal(err)
	}
	c.advance(waitingAskDebounce)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	var open, all int
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(status='open'),0), COUNT(*) FROM inbox_items WHERE source_ref=?`, "waiting:"+sid).Scan(&open, &all); err != nil {
		t.Fatal(err)
	}
	if open != 0 || all != 1 {
		t.Fatalf("waiting rows = %d open of %d, want the closed one only", open, all)
	}
}
