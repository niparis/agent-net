package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A chief whose provider quota is gone cannot work, even though its session
// is online. A new ask goes to the owner path (the Desk, which the CTO reads)
// and is not left to wait for the chief.
func TestChiefQuotaExhaustedRoutesNewAskToOwner(t *testing.T) {
	s, _, chief, asker, _ := chiefRig(t)
	s.setChiefQuota(chief, "codex quota exhausted (weekly window, until 2026-11-15 00:00 UTC)")
	id := chiefAsk(t, s, asker, "Ship it?")
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q with the chief out of quota, want owner", next)
	}
	st, err := s.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Down, "quota exhausted") || !strings.Contains(st.Down, "2026-11-15") {
		t.Fatalf("status down = %q, want the quota reason with the reset time", st.Down)
	}
	if !st.Online {
		t.Fatal("the session is online; only the chief status says it cannot work")
	}
	// No batch goes to a chief that cannot work.
	if _, ok := sendDueChief(t, s, s.now().Add(time.Minute)); ok {
		t.Fatal("a batch went to a chief that has no quota")
	}
}

// The asks that already wait for the chief move to the owner path when the
// chief becomes unavailable, and each gets a log line that says why.
func TestChiefQuotaExhaustedMovesWaitingAsks(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q with a healthy chief, want chief", next)
	}
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("the healthy chief got no batch")
	}
	s.setChiefQuota(chief, "codex quota exhausted (weekly window)")
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q after the chief lost its quota, want owner", next)
	}
	entries, err := s.DeskLog(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || !strings.Contains(entries[0].Text, "chief of staff cannot work") || !strings.Contains(entries[0].Text, "quota exhausted") {
		t.Fatalf("desk log = %+v, want a line that names the quota", entries)
	}
}

// A chief that got its batch and made no sort and showed no harness activity
// for ChiefSilentAfter, with asks waiting, is unavailable.
func TestChiefSilentWithNoActivityIsUnavailable(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("no batch went to the chief")
	}
	c.t = c.t.Add(protocol.ChiefSilentAfter - time.Second)
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q before the silence limit, want chief", next)
	}
	c.t = c.t.Add(2 * time.Second)
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q after the silence limit, want owner", next)
	}
	st, err := s.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Down != protocol.ChiefSilentText(protocol.ChiefSilentAfter) {
		t.Fatalf("status down = %q, want %q", st.Down, protocol.ChiefSilentText(protocol.ChiefSilentAfter))
	}
	// New asks skip the silent chief.
	other := chiefAskKind(t, s, asker, "approval", "Also this?")
	if next := askNext(t, s, other); next != protocol.AskNextOwner {
		t.Fatalf("new ask next = %q while the chief is silent, want owner", next)
	}
	_ = chief
}

// A chief that is still working is not silent, even when it has sorted nothing
// for longer than ChiefSilentAfter: a model turn on a batch can take minutes.
// Its asks stay with it until the activity itself stops for ChiefSilentAfter.
func TestChiefWithRecentHarnessActivityIsNotSilent(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	c.t = c.t.Add(time.Minute)
	if _, ok := sendDueChief(t, s, c.t); !ok {
		t.Fatal("no batch went to the chief")
	}
	c.t = c.t.Add(protocol.ChiefSilentAfter + time.Minute)
	// The chief's harness was active a minute ago.
	if _, err := s.db.Exec(`UPDATE sessions SET harness_activity=? WHERE id=?`, c.t.Add(-time.Minute).UnixNano(), chief); err != nil {
		t.Fatal(err)
	}
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q for a chief with recent activity, want chief", next)
	}
	if st, _ := s.ChiefStatus(); st.Down != "" {
		t.Fatalf("down = %q for a working chief, want none", st.Down)
	}
	// Its activity stops: ChiefSilentAfter later, it is silent.
	c.t = c.t.Add(protocol.ChiefSilentAfter)
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q once the activity stopped, want owner", next)
	}
}

// A silent chief that sorts again is available again at once.
func TestChiefSilentEndsWithASort(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	id := chiefAsk(t, s, asker, "Ship it?")
	c.t = c.t.Add(time.Minute)
	sendDueChief(t, s, c.t)
	c.t = c.t.Add(protocol.ChiefSilentAfter + time.Second)
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ChiefStatus(); st.Down == "" {
		t.Fatal("the silent chief is not marked down")
	}
	c.t = c.t.Add(time.Second)
	// An ask can still reach the chief while it is marked (a race with the
	// tick). Its sort ends the silence.
	if _, err := s.db.Exec(`UPDATE inbox_items SET next_who=? WHERE id=?`, protocol.AskNextChief, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SortAsk(node, protocol.AskSortRequest{SessionID: chief, AskSortArgs: protocol.AskSortArgs{
		Ask: id, Result: protocol.AskSortClose, Reason: "late but done"}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ChiefStatus(); st.Down != "" {
		t.Fatalf("down = %q after a sort, want none", st.Down)
	}
	next := chiefAskKind(t, s, asker, "approval", "Next one?")
	if got := askNext(t, s, next); got != protocol.AskNextChief {
		t.Fatalf("next = %q after the chief sorted, want chief", got)
	}
}

// A silent chief gets one new ask after ChiefRetryAfter as a probe, even when
// it never sorts anything.
func TestChiefSilentRetriesAfterWait(t *testing.T) {
	s, c, _, asker, _ := chiefRig(t)
	chiefAsk(t, s, asker, "Ship it?")
	c.t = c.t.Add(time.Minute)
	sendDueChief(t, s, c.t)
	c.t = c.t.Add(protocol.ChiefSilentAfter + time.Second)
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(protocol.ChiefRetryAfter + time.Second)
	id := chiefAskKind(t, s, asker, "approval", "Probe?")
	if next := askNext(t, s, id); next != protocol.AskNextChief {
		t.Fatalf("next = %q after the retry wait, want chief", next)
	}
}

// When the quota comes back, new asks go to the chief again. The asks that
// moved to the owner path stay there, also after the minute reconcile.
func TestChiefRecoveryRoutesNewAsksToChiefAndKeepsMovedOnes(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	old := chiefAsk(t, s, asker, "Old one?")
	s.setChiefQuota(chief, "codex quota exhausted (weekly window)")
	if err := s.releaseChiefAsks(c.t); err != nil {
		t.Fatal(err)
	}
	during := chiefAskKind(t, s, asker, "approval", "During the outage?")
	for _, id := range []string{old, during} {
		if next := askNext(t, s, id); next != protocol.AskNextOwner {
			t.Fatalf("ask %s next = %q during the outage, want owner", id, next)
		}
	}
	s.setChiefQuota(chief, "")
	fresh := chiefAskKind(t, s, asker, "feature_idea", "After the outage?")
	if next := askNext(t, s, fresh); next != protocol.AskNextChief {
		t.Fatalf("new ask next = %q after recovery, want chief", next)
	}
	c.t = c.t.Add(time.Minute)
	if err := s.reconcileAsks(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{old, during} {
		if next := askNext(t, s, id); next != protocol.AskNextOwner {
			t.Fatalf("ask %s next = %q after recovery, want it to stay with the owner path", id, next)
		}
	}
	if st, _ := s.ChiefStatus(); st.Down != "" {
		t.Fatalf("down = %q after recovery, want none", st.Down)
	}
}

// A usage-limit error on the chief's last turn (the daemon reports it as the
// session's limit) makes the chief unavailable until the limit ends.
func TestChiefHarnessErrorIsUnavailable(t *testing.T) {
	s, c, chief, asker, _ := chiefRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET limited_until=?, limited_reason=? WHERE id=?`, c.t.Add(time.Hour).Unix(), "ChatGPT usage limit", chief); err != nil {
		t.Fatal(err)
	}
	id := chiefAsk(t, s, asker, "Ship it?")
	if next := askNext(t, s, id); next != protocol.AskNextOwner {
		t.Fatalf("next = %q with the chief usage-limited, want owner", next)
	}
	st, _ := s.ChiefStatus()
	if !strings.Contains(st.Down, "harness error") || !strings.Contains(st.Down, "ChatGPT usage limit") {
		t.Fatalf("down = %q, want the harness error", st.Down)
	}
	c.t = c.t.Add(2 * time.Hour)
	if st, _ := s.ChiefStatus(); st.Down != "" {
		t.Fatalf("down = %q after the limit ended, want none", st.Down)
	}
}

// An offline or harness-lost chief says so on the status line.
func TestChiefOfflineStatusReasons(t *testing.T) {
	s, _, chief, _, _ := chiefRig(t)
	if _, err := s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ChiefStatus(); st.Down != "session offline" {
		t.Fatalf("down = %q, want session offline", st.Down)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET harness_lost=1 WHERE id=?`, chief); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ChiefStatus(); st.Down != "harness lost" {
		t.Fatalf("down = %q, want harness lost", st.Down)
	}
}

// The gateway tick reads the quota of the chief's provider on the chief's
// machine (the picker's data). A used-up weekly window makes the chief
// unavailable, and the asks that waited for it move to the owner path.
func TestFlushChiefReadsMachineQuotaAndMovesAsks(t *testing.T) {
	g, node := scopeGateway(t)
	st := g.store
	c := &clock{t: time.Date(2026, 11, 9, 21, 0, 0, 0, time.UTC)}
	st.now = c.now
	chief, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "chief", Model: "gpt-6.1-sol"})
	if err != nil {
		t.Fatal(err)
	}
	asker, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "asker"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	ask := chiefAsk(t, st, asker, "Ship it?")
	if next := askNext(t, st, ask); next != protocol.AskNextChief {
		t.Fatalf("next = %q with a healthy chief, want chief", next)
	}
	reset := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	g.usage.set(node, c.t, protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: c.t.Unix(), Providers: []protocol.QuotaProvider{{Provider: "codex",
		Windows: []protocol.QuotaWindow{{ID: "weekly", Label: "weekly", PercentUsed: 100, ResetsAt: reset.Unix()}}}}}})
	g.flushChief(context.Background())
	if next := askNext(t, st, ask); next != protocol.AskNextOwner {
		t.Fatalf("next = %q after the gateway saw the quota used up, want owner", next)
	}
	status, err := st.ChiefStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.Down, "codex quota exhausted") || !strings.Contains(status.Down, "2026-11-15 00:00 UTC") {
		t.Fatalf("down = %q", status.Down)
	}
	// The quota comes back: the next tick clears it, and new asks reach the chief.
	g.usage.set(node, c.t, protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: c.t.Unix(), Providers: []protocol.QuotaProvider{{Provider: "codex",
		Windows: []protocol.QuotaWindow{{ID: "weekly", Label: "weekly", PercentUsed: 20, ResetsAt: reset.Unix()}}}}}})
	g.flushChief(context.Background())
	fresh := chiefAskKind(t, st, asker, "approval", "After?")
	if next := askNext(t, st, fresh); next != protocol.AskNextChief {
		t.Fatalf("new ask next = %q after the quota came back, want chief", next)
	}
	if next := askNext(t, st, ask); next != protocol.AskNextOwner {
		t.Fatalf("moved ask next = %q, want it to stay with the owner path", next)
	}
}
