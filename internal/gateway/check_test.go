package gateway

import (
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

func TestCheckTwoMoreWithoutProgressStops(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
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
	// A turn is activity, but it changes no message and no open work.
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check 2 = %+v", got)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Announce(mustNode(t, s, b), protocol.ProfileAnnounce{SessionID: b, Task: "more: still first step"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for _, item := range items {
		if item.Kind == "check" && item.Status == "open" {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("Needs you rows = %d, want 1: %+v", open, items)
	}
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 0 {
		t.Fatalf("check after two stalled more answers: %+v", got)
	}
	// Progress (the request is answered) allows checks again.
	if _, _, err := send(s, b, a, "reply", req.ID); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	activity(t, s, b, "pi", "nb")
	c.t = c.t.Add(21 * time.Minute)
	if got := checks(t, s); len(got) != 1 {
		t.Fatalf("check after progress = %+v", got)
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
