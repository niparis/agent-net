package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A recipient on a usage limit cannot reply, so the chase sends it no nudge
// and raises no ask until the limit ends. The wait then counts from the end
// of the limit, not from the request.
func TestOwedReplyLimitedRecipientIsChasedAfterLimitEnds(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Fix the limited session", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=?, limited_until=?, limited_reason='usage limit' WHERE id=?",
		start.UnixNano(), start.Add(time.Hour).Unix(), b); err != nil {
		t.Fatal(err)
	}
	until := start.Add(time.Hour)

	// Idle for 15 minutes, but still limited: no nudge.
	tickOwed(t, s, c, start.Add(15*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 0 || asks != 0 {
		t.Fatalf("while limited: %d nudges, %d asks; want none", nudges, asks)
	}

	// The last second of the limit: still no nudge.
	tickOwed(t, s, c, until.Add(-time.Second))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 0 || asks != 0 {
		t.Fatalf("one second before the limit ends: %d nudges, %d asks; want none", nudges, asks)
	}

	// The limit has ended, but the recipient has had only the same 15 minutes
	// of wait counted from the end of the limit: no nudge yet.
	tickOwed(t, s, c, until.Add(14*time.Minute+59*time.Second))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 0 || asks != 0 {
		t.Fatalf("14m59s after the limit ends: %d nudges, %d asks; want none", nudges, asks)
	}

	// 15 minutes after the limit ends: one nudge, no ask yet.
	tickOwed(t, s, c, until.Add(15*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("15m after the limit ends: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}

	// The ask waits 10 minutes after the nudge.
	tickOwed(t, s, c, until.Add(24*time.Minute+59*time.Second))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("9m59s after the nudge: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}
	tickOwed(t, s, c, until.Add(25*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 1 {
		t.Fatalf("10m after the nudge: %d nudges, %d asks; want 1 and 1", nudges, asks)
	}
}

// A limit that starts after the nudge holds the ask until the limit ends and
// the ask wait has run from there.
func TestOwedReplyLimitStartingAfterNudgeHoldsAsk(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Review the change", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", start.UnixNano(), b); err != nil {
		t.Fatal(err)
	}

	tickOwed(t, s, c, start.Add(15*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("before the limit: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}

	// The limit starts at 16 minutes and lasts two hours.
	until := start.Add(2 * time.Hour)
	c.t = start.Add(16 * time.Minute)
	if _, err := s.db.Exec("UPDATE sessions SET limited_until=?, limited_reason='usage limit' WHERE id=?", until.Unix(), b); err != nil {
		t.Fatal(err)
	}

	// Ten minutes after the nudge, but still limited: no ask.
	tickOwed(t, s, c, start.Add(25*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("limited after the nudge: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}

	tickOwed(t, s, c, until.Add(9*time.Minute+59*time.Second))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("9m59s after the limit ends: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}
	tickOwed(t, s, c, until.Add(10*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 1 {
		t.Fatalf("10m after the limit ends: %d nudges, %d asks; want 1 and 1", nudges, asks)
	}
}

// A limit that ended before the request does not delay the chase.
func TestOwedReplyEndedLimitBeforeRequestChasesAsUsual(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)

	start := c.t
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=?, limited_until=?, limited_reason='usage limit' WHERE id=?",
		start.UnixNano(), start.Add(-time.Hour).Unix(), b); err != nil {
		t.Fatal(err)
	}
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "Task after an old limit", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}

	tickOwed(t, s, c, start.Add(15*time.Minute))
	if nudges, asks := owedCounts(t, s, req.ID); nudges != 1 || asks != 0 {
		t.Fatalf("after an old limit: %d nudges, %d asks; want 1 nudge and no ask", nudges, asks)
	}
}
