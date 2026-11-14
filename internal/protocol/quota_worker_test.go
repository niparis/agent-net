package protocol

import "testing"

// The quota line names the window the picker's floor reads for the worker's
// model: the most used window of the provider that serves it.
func TestQuotaWorkerReadingNamesTheWindowTheFloorUses(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{{Provider: "agy", Plan: "google", Windows: []QuotaWindow{
		{ID: "gemini_argon_weekly", Label: "Gemini Argon weekly", Kind: "weekly", PercentUsed: 12},
		{ID: "gemini_5h", Label: "Gemini 5-hour", Kind: "session", PercentUsed: 40},
		{ID: "claude_gpt_weekly", Label: "Claude/GPT weekly", Kind: "weekly", PercentUsed: 90},
	}}}}
	if name, pct, ok := QuotaWorkerReading(q, "agy", "gemini-4-argon-high"); name != "agy Gemini 5-hour" || pct != 40 || !ok {
		t.Fatalf("QuotaWorkerReading = %q, %v, %v; want the 5-hour window", name, pct, ok)
	}
	if name, pct, ok := QuotaWorkerReading(q, "agy", "claude-sonnet-5-5-medium"); name != "agy Claude/GPT weekly" || pct != 90 || !ok {
		t.Fatalf("Claude through agy: %q, %v, %v", name, pct, ok)
	}
}

// A provider agent-net reads but has no usable reading is not a reading, and a
// harness with no quota provider names none.
func TestQuotaWorkerReadingSaysNoReading(t *testing.T) {
	if name, _, ok := QuotaWorkerReading(nil, "agy", "gemini-4-argon-high"); name != "agy" || ok {
		t.Fatalf("no reading: %q, %v", name, ok)
	}
	stale := &Quotas{Providers: []QuotaProvider{{Provider: "agy", Stale: true, Windows: []QuotaWindow{{ID: "gemini_weekly", PercentUsed: 10}}}}}
	if name, _, ok := QuotaWorkerReading(stale, "agy", "gemini-4-argon-high"); name != "agy" || ok {
		t.Fatalf("stale reading: %q, %v", name, ok)
	}
	if name, _, ok := QuotaWorkerReading(&Quotas{}, "pi", "gpt-6"); name != "" || ok {
		t.Fatalf("pi has no quota provider: %q, %v", name, ok)
	}
}
