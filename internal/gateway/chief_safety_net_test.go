package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Front door step (c), decision 0150: no timer sends an ask to the owner while
// the chief is live. A safety net covers a chief that is down: presence plus
// missed wakes. Claimed asks that go quiet ping the chief, never the owner.

const testStuckLimit = 12 * time.Hour

func setCategory(t *testing.T, s *Store, id, category string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE inbox_items SET category=? WHERE id=?`, category, id); err != nil {
		t.Fatal(err)
	}
}

// A chief that is live and keeps working never loses an ask by a timer, however
// long the ask waits.
func TestLiveAnsweringChiefNeverSendsAskToOwnerByTimer(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	setCategory(t, s, id, protocol.CategorySecurity)
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("the live chief got no batch")
	}
	// Three hours in five-minute steps. The chief's harness shows activity
	// the whole time, so it is working, not silent.
	for range 36 {
		c.t = c.t.Add(5 * time.Minute)
		if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=? WHERE id=?`, c.t.UnixNano(), chief); err != nil {
			t.Fatal(err)
		}
		if err := s.reconcileAsks(); err != nil {
			t.Fatal(err)
		}
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q after 3 hours with a live chief, want chief", next)
	}
	if entries, _ := s.DeskLog(10); len(entries) != 0 {
		t.Fatalf("desk log = %+v, want no timer entry", entries)
	}
}

// The batch text no longer promises that an ask goes to the owner after a
// wait.
func TestChiefBatchTextHasNoOwnerTimer(t *testing.T) {
	s, c, _, asker, _ := chiefRig(t)
	chiefAsk(t, s, asker, "Ship it?")
	c.t = c.t.Add(time.Minute)
	body, ok := sendDueChief(t, s, c.t)
	if !ok {
		t.Fatal("no batch")
	}
	if strings.Contains(body, "15 minutes") || strings.Contains(body, "goes to the owner") {
		t.Fatalf("batch text still names an owner timer: %q", body)
	}
}

// With the chief offline, an ask in security, outage or money goes to the owner
// after one missed wake (10 minutes). Every other ask goes after two.
func TestDownChiefSafetyNetByCategory(t *testing.T) {
	for _, cat := range []string{protocol.CategorySecurity, protocol.CategoryOutage, protocol.CategoryMoney} {
		t.Run(cat, func(t *testing.T) {
			s, c, chief, asker, _ := chiefRig(t)
			urgent := chiefAsk(t, s, asker, "Is this deploy ok?")
			setCategory(t, s, urgent, cat)
			plain := chiefAskKind(t, s, asker, "check", "Which name?")
			if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, chief); err != nil {
				t.Fatal(err)
			}
			steps := []struct {
				after        time.Duration
				urgent, rest string
			}{
				{protocol.ChiefWakeInterval - time.Minute, protocol.AskNextChief, protocol.AskNextChief},
				{time.Minute, protocol.AskNextOwner, protocol.AskNextChief}, // one missed wake
				{protocol.ChiefWakeInterval - time.Minute, protocol.AskNextOwner, protocol.AskNextChief},
				{time.Minute, protocol.AskNextOwner, protocol.AskNextOwner}, // two missed wakes
			}
			for i, st := range steps {
				c.t = c.t.Add(st.after)
				if err := s.reconcileAsks(); err != nil {
					t.Fatal(err)
				}
				if got := askNext(t, s, urgent); got != st.urgent {
					t.Fatalf("step %d: %s ask next = %q, want %q", i, cat, got, st.urgent)
				}
				if got := askNext(t, s, plain); got != st.rest {
					t.Fatalf("step %d: normal ask next = %q, want %q", i, got, st.rest)
				}
			}
			entries, _ := s.DeskLog(10)
			if len(entries) != 2 || !strings.Contains(entries[0].Text, "offline") {
				t.Fatalf("desk log = %+v, want one line per released ask that says the chief is offline", entries)
			}
		})
	}
}

// A chief that is online but cannot work (no quota) is under the same rule,
// not released at once.
func TestChiefWithoutQuotaWaitsForMissedWakes(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	s.setChiefQuota(chief, "codex quota exhausted (weekly window)")
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q at once, want chief (no missed wake yet)", next)
	}
	c.t = c.t.Add(2 * protocol.ChiefWakeInterval)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q after two missed wakes, want owner", next)
	}
	entries, _ := s.DeskLog(10)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "quota exhausted") {
		t.Fatalf("desk log = %+v, want the quota reason", entries)
	}
}

// An offline chief whose harness can be woken gets its batch: that message is
// the wake. A wake it does not answer counts as missed.
func TestOfflineWakeableChiefGetsBatchAsWake(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("no batch went to the offline, wakeable chief")
	}
	c.t = c.t.Add(2 * protocol.ChiefWakeInterval)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q after two missed wakes, want owner", next)
	}
}

// A harness that cannot be woken (Pi) has presence and batches only. Online
// with an unanswered batch and no activity, it is under the same rule; offline
// it gets no batch, and its asks still reach the owner by the same clock.
func TestUnwakeableChiefUsesPresenceAndUnansweredBatches(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET harness='pi' WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	urgent := chiefAsk(t, s, asker, "Is this deploy ok?")
	setCategory(t, s, urgent, protocol.CategoryMoney)
	plain := chiefAskKind(t, s, asker, "check", "Which name?")
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("the online Pi chief got no batch")
	}
	// The batch stays unanswered for one wake interval: the chief is silent.
	c.t = c.t.Add(protocol.ChiefWakeInterval + time.Second)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if got := askNext(t, s, urgent); got != protocol.AskNextOwner {
		t.Fatalf("money ask next = %q after one unanswered batch, want owner", got)
	}
	if got := askNext(t, s, plain); got != protocol.AskNextChief {
		t.Fatalf("normal ask next = %q after one unanswered batch, want chief", got)
	}
	c.t = c.t.Add(protocol.ChiefWakeInterval)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if got := askNext(t, s, plain); got != protocol.AskNextOwner {
		t.Fatalf("normal ask next = %q after two unanswered batches, want owner", got)
	}

	// Offline: no batch can wake it.
	s2, c2, chief2, asker2, _ := chiefRig(t)
	if _, err := s2.db.Exec(`UPDATE sessions SET harness='pi', online=0 WHERE id=?`, chief2); err != nil {
		t.Fatal(err)
	}
	// The ask must wait for a chief that was live when it was opened.
	id := chiefAsk(t, s2, asker2, "Ship it?")
	if _, err := s2.db.Exec(`UPDATE inbox_items SET next_who=?, chief_at=? WHERE id=?`, protocol.AskNextChief, c2.t.UnixNano(), id); err != nil {
		t.Fatal(err)
	}
	c2.t = c2.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s2, c2.t); ok {
		t.Fatal("a batch went to an offline Pi chief, which cannot be woken")
	}
	c2.t = c2.t.Add(2 * protocol.ChiefWakeInterval)
	if err := s2.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	if got := askNext(t, s2, id); got != protocol.AskNextOwner {
		t.Fatalf("ask next = %q for an offline Pi chief after two wake intervals, want owner", got)
	}
}

// A claimed ask (waiting on the chief, or on a session the chief chose) that has
// gone quiet for the stuck-work limit pings the chief once per limit. It never
// goes to the owner, and the message says how long it has been open.
func TestStuckClaimedAskPingsChiefNotOwner(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("no batch")
	}
	c.t = c.t.Add(testStuckLimit - 2*time.Minute)
	msgs, err := s.chiefStuckWork(c.t, testStuckLimit)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("before the limit: %d messages, err %v", len(msgs), err)
	}
	c.t = c.t.Add(3*time.Minute + time.Hour)
	if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=? WHERE id=?`, c.t.UnixNano(), chief); err != nil {
		t.Fatal(err)
	}
	msgs, err = s.chiefStuckWork(c.t, testStuckLimit)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("past the limit: %d messages, err %v", len(msgs), err)
	}
	m := msgs[0]
	if m.To != chief || !strings.Contains(m.Body, id) || !strings.Contains(m.Body, "still open, 13 hours") {
		t.Fatalf("ping to %q = %q, want the chief and %q", m.To, m.Body, "still open, 13 hours")
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q after the ping, want chief", next)
	}
	// One ping per limit, not one per tick.
	if again, err := s.chiefStuckWork(c.t.Add(time.Minute), testStuckLimit); err != nil || len(again) != 0 {
		t.Fatalf("second tick sent %d messages, err %v", len(again), err)
	}
	c.t = c.t.Add(testStuckLimit)
	if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=? WHERE id=?`, c.t.UnixNano(), chief); err != nil {
		t.Fatal(err)
	}
	if again, err := s.chiefStuckWork(c.t, testStuckLimit); err != nil || len(again) != 1 || !strings.Contains(again[0].Body, "still open, 25 hours") {
		t.Fatalf("next limit: %+v, err %v", again, err)
	}
}

// The same quiet ask shows in the morning brief wake as "still open, N hours".
func TestMorningBriefShowsStillOpenAsks(t *testing.T) {
	s, c, chief, other := briefRig(t)
	setBriefZone(t, s, "Europe/Paris")
	id := chiefAsk(t, s, other, "Ship it?")
	// 2026-10-29 09:00 Paris is 08:00 UTC: 20 hours after the ask.
	c.t = time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=? WHERE id=?`, c.t.UnixNano(), chief); err != nil {
		t.Fatal(err)
	}
	m, sent, err := s.checkDailyBrief(c.t)
	if err != nil || !sent {
		t.Fatalf("brief sent=%v err=%v", sent, err)
	}
	if !strings.Contains(m.Body, id) || !strings.Contains(m.Body, "still open, 20 hours") {
		t.Fatalf("brief wake = %q, want the ask and %q", m.Body, "still open, 20 hours")
	}
}

// An item with the CTO that has had no move for the stuck-work limit goes back
// to the chief: the CTO acts only in owner conversations.
func TestWithCTOItemIdlePastLimitGoesBackToChief(t *testing.T) {
	s, c, _, asker, cto, _ := deskRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who=? WHERE id=?`, cto, id); err != nil {
		t.Fatal(err)
	}
	if _, _, state, _, _ := itemRow(t, s, id); state != protocol.DeskWithCTO {
		t.Fatalf("state = %q, want with_cto", state)
	}
	c.t = c.t.Add(testStuckLimit - time.Minute)
	if _, err := s.chiefStuckWork(c.t, testStuckLimit); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != cto {
		t.Fatalf("next = %q before the limit, want the CTO", next)
	}
	c.t = c.t.Add(2 * time.Minute)
	if _, err := s.chiefStuckWork(c.t, testStuckLimit); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q after the limit, want chief", next)
	}
	if _, owner, state, _, _ := itemRow(t, s, id); owner != protocol.DeskOwnerChief || state != protocol.DeskVerifying {
		t.Fatalf("owner %q state %q, want chief and verifying", owner, state)
	}
	entries, _ := s.DeskLog(10)
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "back with the chief") {
		t.Fatalf("desk log = %+v, want the back-with-the-chief line", entries)
	}
	// It starts a fresh clock: it waits for the chief's batch like a new ask.
	var sent int64
	if err := s.db.QueryRow(`SELECT chief_sent_at FROM inbox_items WHERE id=?`, id).Scan(&sent); err != nil || sent != 0 {
		t.Fatalf("chief_sent_at = %d, err %v, want 0", sent, err)
	}
}
