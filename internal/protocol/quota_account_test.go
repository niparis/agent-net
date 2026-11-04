package protocol

import (
	"strings"
	"testing"
	"time"
)

const agyQuotaFailure = "Antigravity result ERROR error: Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 160h24m5s."

func TestAccountQuotaBlockFromFailure(t *testing.T) {
	for _, tc := range []struct {
		harness, failure string
		provider         string
		wait             time.Duration
	}{
		{"agy", agyQuotaFailure, "agy", 160*time.Hour + 24*time.Minute + 5*time.Second},
		{"agy", "Antigravity result ERROR error: Individual quota reached. Resets in 159h25m8s.", "agy", 159*time.Hour + 25*time.Minute + 8*time.Second},
		{"agy", "quota exceeded for this account; resets in 2d 3h", "agy", 51 * time.Hour},
		// No reset time: nothing to block until.
		{"agy", "Individual quota reached. Please upgrade.", "", 0},
		// A rate limit is short and not an account quota.
		{"agy", "HTTP 429 rate limit, resets in 30s", "", 0},
		// Other harnesses read their windows from quota-axi.
		{"claude", agyQuotaFailure, "", 0},
		{"agy", "Antigravity exited without a successful result", "", 0},
	} {
		provider, wait, ok := AccountQuotaBlock(tc.harness, tc.failure)
		if provider != tc.provider || wait != tc.wait || ok != (tc.provider != "") {
			t.Errorf("%s %q: got %q %v %v; want %q %v", tc.harness, tc.failure, provider, wait, ok, tc.provider, tc.wait)
		}
	}
}

func TestAccountQuotaBlockFailureCountsAsInfra(t *testing.T) {
	if got := WorkerOutcomeDetail(StopRunFailed, "", nil, agyQuotaFailure); got != OutcomeInfra {
		t.Fatalf("outcome %q, want %q", got, OutcomeInfra)
	}
}

func TestWithQuotaBlocksRefusesEveryAgyModel(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	reset := now.Add(160 * time.Hour)
	blocks := map[string]time.Time{"agy": reset}
	reading := &Quotas{Providers: []QuotaProvider{{Provider: "agy", Windows: []QuotaWindow{{ID: "gemini_argon_weekly", PercentUsed: 10}, {ID: "claude_gpt_weekly", PercentUsed: 10}}}}}
	for name, q := range map[string]*Quotas{
		"with a reading":     reading,
		"without a reading":  nil,
		"with a stale agy":   {Providers: []QuotaProvider{{Provider: "agy", Stale: true}}},
		"with an agy error":  {Providers: []QuotaProvider{{Provider: "agy", Error: "cannot read"}}},
		"with other readers": {Providers: []QuotaProvider{{Provider: "claude", Windows: []QuotaWindow{{ID: "seven_day", PercentUsed: 50}}}}},
	} {
		got := WithQuotaBlocks(q, blocks, now)
		for _, model := range []string{"gemini-4-argon-high", "claude-opus-5-5-high", "gpt-6-agy", ""} {
			msg := QuotaRefusal(got, "agy", model)
			want := "agy quota used up until 2026-11-11 04:00 UTC"
			if !strings.Contains(msg, want) || !strings.Contains(msg, "pick another harness") {
				t.Errorf("%s, %q: refusal %q", name, model, msg)
			}
		}
		if QuotaRefusal(got, "claude", "claude-opus-5-5") != "" {
			t.Errorf("%s: claude refused too", name)
		}
	}
	if len(reading.Providers[0].Windows) != 2 {
		t.Fatal("WithQuotaBlocks changed the reading it was given")
	}
	// The block ends at the reset time.
	if msg := QuotaRefusal(WithQuotaBlocks(reading, blocks, reset), "agy", "gemini-4-argon-high"); msg != "" {
		t.Fatalf("refusal after the reset: %q", msg)
	}
	// The picker skips agy while the block holds.
	_, _, _, blocked := workerQuotaRoom(WithQuotaBlocks(reading, blocks, now), "agy", "claude-opus-5-5-high")
	if !blocked {
		t.Fatal("picker still offers agy")
	}
}

func TestPerFamilyAgyWindowBlocksOnlyThatFamily(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{{Provider: "agy", Windows: []QuotaWindow{{ID: "gemini_argon_weekly", Exhausted: true}, {ID: "claude_gpt_weekly", PercentUsed: 10}}}}}
	if QuotaRefusal(q, "agy", "gemini-4-argon-high") == "" {
		t.Fatal("argon not refused")
	}
	for _, model := range []string{"claude-opus-5-5-high", "gemini-4-flash"} {
		if msg := QuotaRefusal(q, "agy", model); msg != "" {
			t.Errorf("%s refused: %q", model, msg)
		}
	}
}
