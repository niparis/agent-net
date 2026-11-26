package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// tickOwed runs the owed-reply check at a test time.
func tickOwed(t *testing.T, s *Store, c *clock, at time.Time) {
	t.Helper()
	c.set(at)
	if _, err := s.checkOwedReplies(); err != nil {
		t.Fatal(err)
	}
}

// owedCounts returns how many nudges one request has, and 1 once its owed
// reply reached the ask: agent-net raised it, so it is a log line, not a
// stored item (decision 0196).
func owedCounts(t *testing.T, s *Store, logs *[]string, reqID string) (nudges, asks int) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE parent_id=? AND sender='agent-net'`, reqID).Scan(&nudges); err != nil {
		t.Fatal(err)
	}
	for _, line := range *logs {
		if strings.HasPrefix(line, "desk.dropped ") && strings.Contains(line, " ref owed-ask:"+reqID+" ") {
			return nudges, 1
		}
	}
	return nudges, 0
}

// A progress reply with follow_up_at holds the chase until that time. The
// nudge comes at the time, and the ask ten minutes after the nudge.
func TestOwedReplyProgressFollowUpDefersNudgeAndAsk(t *testing.T) {
	s, c := newTestStore(t)
	logs := captureLog(s)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Run the release", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	c.set(start.Add(time.Minute))
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "The release runs about 45 minutes", Progress: true, FollowUpAt: "45m"}); err != nil {
		t.Fatal(err)
	}

	// 30 minutes in: the promised time (46 minutes) has not come.
	tickOwed(t, s, c, start.Add(30*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 0 || asks != 0 {
		t.Fatalf("before the promised time: %d nudges, %d asks; want none", nudges, asks)
	}

	// At the promised time: one nudge.
	tickOwed(t, s, c, start.Add(46*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("at the promised time: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}

	// Nine minutes after the nudge: no ask yet.
	tickOwed(t, s, c, start.Add(55*time.Minute))
	if _, asks := owedCounts(t, s, logs, req.ID); asks != 0 {
		t.Fatalf("nine minutes after the nudge: %d asks, want 0", asks)
	}

	// Ten minutes after the nudge: one ask.
	tickOwed(t, s, c, start.Add(56*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 1 || asks != 1 {
		t.Fatalf("ten minutes after the nudge: %d nudges, %d asks; want 1 and 1", nudges, asks)
	}
}

// A progress reply can come after the nudge. Its promised time stops the ask
// that the nudge had started, and the chase resumes after that time.
func TestOwedReplyProgressFollowUpStopsAskAfterNudge(t *testing.T) {
	s, c := newTestStore(t)
	logs := captureLog(s)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Run the release", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	tickOwed(t, s, c, start.Add(15*time.Minute))
	if nudges, _ := owedCounts(t, s, logs, req.ID); nudges != 1 {
		t.Fatalf("at 15 minutes: %d nudges, want 1", nudges)
	}

	c.set(start.Add(16 * time.Minute))
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "About an hour more", Progress: true, FollowUpAt: "1h"}); err != nil {
		t.Fatal(err)
	}

	// The ask would have been due at 25 minutes. The promise holds it.
	tickOwed(t, s, c, start.Add(25*time.Minute))
	if _, asks := owedCounts(t, s, logs, req.ID); asks != 0 {
		t.Fatalf("at 25 minutes, under the promise: %d asks, want 0", asks)
	}

	// The promised time (76 minutes) passed: the ask comes ten minutes later.
	tickOwed(t, s, c, start.Add(76*time.Minute))
	if _, asks := owedCounts(t, s, logs, req.ID); asks != 0 {
		t.Fatalf("at the promised time: %d asks, want 0", asks)
	}
	tickOwed(t, s, c, start.Add(86*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 1 || asks != 1 {
		t.Fatalf("ten minutes after the promised time: %d nudges, %d asks; want 1 and 1", nudges, asks)
	}
}

// A progress reply without follow_up_at holds the chase for the grace period,
// counted from that reply. The default grace is two hours.
func TestOwedReplyProgressWithoutFollowUpDefersByGrace(t *testing.T) {
	s, c := newTestStore(t)
	logs := captureLog(s)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Run the release", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	c.set(start.Add(time.Minute))
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Still working", Progress: true}); err != nil {
		t.Fatal(err)
	}

	// One hour after the reply: still inside the grace.
	tickOwed(t, s, c, start.Add(61*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 0 || asks != 0 {
		t.Fatalf("inside the grace: %d nudges, %d asks; want none", nudges, asks)
	}

	// Two hours after the reply: one nudge, then one ask ten minutes later.
	tickOwed(t, s, c, start.Add(121*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("after the grace: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}
	tickOwed(t, s, c, start.Add(131*time.Minute))
	if nudges, asks := owedCounts(t, s, logs, req.ID); nudges != 1 || asks != 1 {
		t.Fatalf("ten minutes after the grace nudge: %d nudges, %d asks; want 1 and 1", nudges, asks)
	}
}

// messages.owed_reply_progress_grace changes the grace that a progress reply
// without follow_up_at gets.
func TestOwedReplyProgressGraceSetting(t *testing.T) {
	s, c := newTestStore(t)
	logs := captureLog(s)
	a, b := twoSessions(t, s)
	if _, err := s.WriteSettings([]SettingsChange{{Key: "messages.owed_reply_progress_grace", Level: "network", Scope: "network", Value: settingValue(`"30m"`)}}, "owner", "test grace"); err != nil {
		t.Fatal(err)
	}

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Run the release", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	c.set(start.Add(time.Minute))
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Still working", Progress: true}); err != nil {
		t.Fatal(err)
	}

	tickOwed(t, s, c, start.Add(30*time.Minute))
	if nudges, _ := owedCounts(t, s, logs, req.ID); nudges != 0 {
		t.Fatalf("inside the 30 minute grace: %d nudges, want 0", nudges)
	}
	tickOwed(t, s, c, start.Add(31*time.Minute))
	if nudges, _ := owedCounts(t, s, logs, req.ID); nudges != 1 {
		t.Fatalf("after the 30 minute grace: %d nudges, want 1", nudges)
	}
}

// The latest progress reply decides. A later reply without a time replaces an
// earlier promise.
func TestOwedReplyLatestProgressReplyDecides(t *testing.T) {
	s, c := newTestStore(t)
	logs := captureLog(s)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Run the release", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	c.set(start.Add(time.Minute))
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Three hours", Progress: true, FollowUpAt: "3h"}); err != nil {
		t.Fatal(err)
	}
	c.set(start.Add(2 * time.Minute))
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "Still working, no time", Progress: true}); err != nil {
		t.Fatal(err)
	}

	// The later reply's grace (two hours from minute 2) decides, not the three hour promise.
	tickOwed(t, s, c, start.Add(121*time.Minute))
	if nudges, _ := owedCounts(t, s, logs, req.ID); nudges != 0 {
		t.Fatalf("at 121 minutes: %d nudges, want 0", nudges)
	}
	tickOwed(t, s, c, start.Add(122*time.Minute))
	if nudges, _ := owedCounts(t, s, logs, req.ID); nudges != 1 {
		t.Fatalf("at 122 minutes: %d nudges, want 1", nudges)
	}
}

// The progress follow-up is an owed-reply hold. It must not raise the chief's
// follow-up ask that a send's follow_up_at raises.
func TestOwedReplyProgressFollowUpIsNotAChiefFollowUp(t *testing.T) {
	s, c, _, a, b, _ := followUpRig(t)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Run the release", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "About five minutes", Progress: true, FollowUpAt: "5m"}); err != nil {
		t.Fatal(err)
	}

	c.advance(10 * time.Minute)
	tickFollowUps(t, s)
	if got := followUpAsks(t, s); len(got) != 0 {
		t.Fatalf("progress follow-up raised %d chief asks, want 0: %+v", len(got), got)
	}
}
