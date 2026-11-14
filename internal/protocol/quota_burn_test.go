package protocol

import (
	"strings"
	"testing"
	"time"
)

// burnFixture is a Codex-like provider with a five hour window at 60 percent,
// resetting in two hours. Five workers started 20 minutes ago and hold 600k
// tokens each, so 3M tokens were used in the window: the window is worth about
// 50k tokens a percent, and 2M tokens are left. Finished runs lasted 30 minutes.
func burnFixture(percent float64, tokensEach int64) (*Quotas, time.Time) {
	now := time.Unix(2_000_000, 0)
	var runs []BurnRun
	for i := 0; i < 5; i++ {
		runs = append(runs, BurnRun{StartedAt: now.Add(-20 * time.Minute).Unix(), Tokens: tokensEach})
	}
	// Two finished runs set the mean run time (30 minutes). They are outside
	// the window, so they do not count as its usage.
	runs = append(runs,
		BurnRun{StartedAt: now.Add(-9 * time.Hour).Unix(), EndedAt: now.Add(-9*time.Hour + 30*time.Minute).Unix(), Tokens: 1},
		BurnRun{StartedAt: now.Add(-8 * time.Hour).Unix(), EndedAt: now.Add(-8*time.Hour + 30*time.Minute).Unix(), Tokens: 1})
	q := &Quotas{
		GeneratedAt: now.Unix(),
		Providers: []QuotaProvider{{Provider: "codex", Windows: []QuotaWindow{
			{ID: "five_hour", Kind: "five_hour", PercentUsed: percent, ResetsAt: now.Add(2 * time.Hour).Unix()},
		}}},
		Burn: &QuotaBurnSet{Now: now.Unix(), ByProvider: map[string]QuotaBurn{"codex": {Runs: runs}}},
	}
	return q, now
}

func TestQuotaBurnHoldsWhenRunningWorkersWouldExhaustTheWindow(t *testing.T) {
	q, _ := burnFixture(60, 600_000)
	reason, hold := QuotaBurnHold(q, "codex", "gpt-6.1-sol")
	if !hold {
		t.Fatal("five workers burning 30k tokens a minute each must hold a sixth: 2M tokens are left, 5.4M are needed")
	}
	for _, want := range []string{"codex", "five_hour", "resets in 2h", "running"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q lacks %q", reason, want)
		}
	}
}

func TestQuotaBurnLeavesAProviderWithPlentyOfQuotaAlone(t *testing.T) {
	// The same burn at 5 percent used: 3M tokens are 5 percent, so 57M are left.
	q, _ := burnFixture(5, 600_000)
	if reason, hold := QuotaBurnHold(q, "codex", "gpt-6.1-sol"); hold {
		t.Fatalf("plenty of quota must not hold: %s", reason)
	}
	// A provider with no burn data is never held.
	if reason, hold := QuotaBurnHold(q, "claude", ""); hold {
		t.Fatalf("a provider with no workers must not hold: %s", reason)
	}
	// A nil reading and a reading with no burn hold nothing.
	if _, hold := QuotaBurnHold(nil, "codex", ""); hold {
		t.Fatal("nil quotas held")
	}
	if _, hold := QuotaBurnHold(&Quotas{}, "codex", ""); hold {
		t.Fatal("empty quotas held")
	}
}

func TestQuotaBurnNeverHoldsTheFirstWorker(t *testing.T) {
	q, now := burnFixture(60, 600_000)
	// Only finished runs: nothing runs now, so there is no rate to project.
	b := q.Burn.ByProvider["codex"]
	b.Runs = b.Runs[5:]
	q.Burn.ByProvider["codex"] = b
	_ = now
	if reason, hold := QuotaBurnHold(q, "codex", ""); hold {
		t.Fatalf("with no running worker the start must go: %s", reason)
	}
}

func TestQuotaBurnLearnedBudgetStandsInWhenThereIsNoReading(t *testing.T) {
	// quota-axi does not see the account limit: no codex reading. A failure
	// taught the budget: 4M tokens in a five hour window. Five workers hold 3M.
	q, now := burnFixture(60, 600_000)
	q.Providers = nil
	b := q.Burn.ByProvider["codex"]
	b.Budgets = []QuotaBudget{{WindowSeconds: int64(5 * time.Hour / time.Second), Tokens: 4_000_000}}
	q.Burn.ByProvider["codex"] = b
	reason, hold := QuotaBurnHold(q, "codex", "")
	if !hold {
		t.Fatal("1M tokens left against 5.4M needed must hold")
	}
	if !strings.Contains(reason, "learned") {
		t.Errorf("reason must say the budget was learned: %q", reason)
	}
	// A bigger budget lets the start go.
	b.Budgets[0].Tokens = 100_000_000
	q.Burn.ByProvider["codex"] = b
	if reason, hold := QuotaBurnHold(q, "codex", ""); hold {
		t.Fatalf("a large learned budget must not hold: %s", reason)
	}
	_ = now
}

func TestLearnQuotaBudgetReadsTheWindowBeforeTheFailure(t *testing.T) {
	failed := time.Unix(5_000_000, 0)
	runs := []BurnRun{
		// Inside the five hour window: counts whole.
		{StartedAt: failed.Add(-2 * time.Hour).Unix(), EndedAt: failed.Add(-time.Hour).Unix(), Tokens: 1_000_000},
		// Began before the window and lasted two hours, one of them inside.
		{StartedAt: failed.Add(-6 * time.Hour).Unix(), EndedAt: failed.Add(-4 * time.Hour).Unix(), Tokens: 400_000},
		// Still running: counts until the failure.
		{StartedAt: failed.Add(-time.Hour).Unix(), Tokens: 200_000},
		// Long before: does not count.
		{StartedAt: failed.Add(-30 * time.Hour).Unix(), EndedAt: failed.Add(-29 * time.Hour).Unix(), Tokens: 9_000_000},
	}
	b, ok := LearnQuotaBudget(runs, failed, failed.Add(3*time.Hour))
	if !ok || b.WindowSeconds != 5*3600 || b.Tokens != 1_400_000 {
		t.Fatalf("budget %+v ok=%v", b, ok)
	}
	// A delay of 134 hours is a weekly window.
	if b, _ := LearnQuotaBudget(runs, failed, failed.Add(134*time.Hour)); b.WindowSeconds != 7*24*3600 || b.Tokens != 10_600_000 {
		t.Fatalf("weekly budget %+v", b)
	}
	// With no tokens there is nothing to learn.
	if _, ok := LearnQuotaBudget(nil, failed, failed.Add(time.Hour)); ok {
		t.Fatal("learned from nothing")
	}
}
