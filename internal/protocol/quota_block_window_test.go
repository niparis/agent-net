package protocol

import (
	"strings"
	"testing"
	"time"
)

// agyBlockReading is an agy reading: Argon weekly at 40 percent, Gemini weekly
// at 24, Gemini 5-hour at 0 and Claude/GPT weekly exhausted. The Gemini 5-hour
// window resets before the weekly ones.
func agyBlockReading(generated, reset time.Time, argonUsed float64) *Quotas {
	return &Quotas{GeneratedAt: generated.Unix(), Providers: []QuotaProvider{{Provider: "agy", Windows: []QuotaWindow{
		{ID: "gemini_argon_weekly", Label: "Gemini Argon weekly", Kind: "weekly", PercentUsed: argonUsed, ResetsAt: reset.Unix()},
		{ID: "gemini_weekly", Label: "Gemini weekly", Kind: "weekly", PercentUsed: 24, ResetsAt: reset.Unix()},
		{ID: "gemini_5h", Label: "Gemini 5-hour", Kind: "session", PercentUsed: 0, ResetsAt: generated.Add(5 * time.Hour).Unix()},
		{ID: "claude_gpt_weekly", Label: "Claude/GPT weekly", Kind: "weekly", PercentUsed: 100, Exhausted: true, ResetsAt: reset.Unix()},
	}}}}
}

// A run that reports the Argon window out of quota blocks that window only.
// Flash keeps its allowance, and the picker skips Argon.
func TestRunBlockOnOneAgyWindowLeavesOtherModels(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 20, 0, 0, time.UTC)
	until := now.Add(160 * time.Hour)
	reading := agyBlockReading(now.Add(-time.Minute), until, 40)
	argon, ok := QuotaBlockWindowFor(reading, "agy", "gemini-4-argon-high", until)
	if !ok || argon.ID != "gemini_argon_weekly" {
		t.Fatalf("argon window: %+v, %v", argon, ok)
	}
	blocks := []QuotaBlock{{Provider: "agy", WindowID: argon.ID, WindowLabel: argon.Label, Until: until, Recorded: now}}
	q := WithQuotaBlocks(reading, blocks, now)
	if r := QuotaRefusal(q, "agy", "gemini-3.8-flash-low"); r != "" {
		t.Fatalf("flash refused: %q", r)
	}
	want := "agy Gemini Argon weekly used up until 2026-11-15 09:20 UTC; other agy models may still be used"
	if r := QuotaRefusal(q, "agy", "gemini-4-argon-high"); r != want {
		t.Fatalf("argon refusal %q; want %q", r, want)
	}
	candidates := []WorkerModelCandidate{
		{Name: "argon agy", Harness: "agy", Model: "gemini-4-argon-high"},
		{Name: "flash agy", Harness: "agy", Model: "gemini-3.8-flash-low"},
	}
	scores := []ModelScore{{Match: "gemini-4-argon", Score: 30}, {Match: "gemini-3-8-flash", Score: 30}}
	for i := 0; i < 4; i++ {
		got, err := ChooseWorkerModel(candidates, "normal", scores, nil, q, nil, &WorkerRotation{})
		if err != nil || got.Name != "flash agy" {
			t.Fatalf("picker: %+v, %v", got, err)
		}
	}
}

// A model that no window of its provider serves has no window to block, so the
// block covers every model of the provider.
func TestRunBlockWithoutMatchingWindowBlocksProvider(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 20, 0, 0, time.UTC)
	until := now.Add(160 * time.Hour)
	reading := agyBlockReading(now.Add(-time.Minute), until, 40)
	if w, ok := QuotaBlockWindowFor(reading, "agy", "mystery-model", until); ok {
		t.Fatalf("window for an unserved model: %+v", w)
	}
	if _, ok := QuotaBlockWindowFor(reading, "agy", "", until); ok {
		t.Fatal("window for an empty model")
	}
	q := WithQuotaBlocks(reading, []QuotaBlock{{Provider: "agy", Until: until, Recorded: now}}, now)
	for _, model := range []string{"gemini-3.8-flash-low", "gemini-4-argon-high", "claude-opus-5-5-high"} {
		if r := QuotaRefusal(q, "agy", model); !strings.Contains(r, "pick another harness") {
			t.Errorf("%s not refused: %q", model, r)
		}
	}
	if r := QuotaRefusal(q, "claude", "claude-opus-5-5"); r != "" {
		t.Errorf("claude refused: %q", r)
	}
}

// Of the windows that serve a model, the most specific family wins, and of
// equally specific ones the window whose reset is nearest the run's reset.
func TestQuotaBlockWindowForPicksTheWindow(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 20, 0, 0, time.UTC)
	until := now.Add(160 * time.Hour)
	reading := agyBlockReading(now, until, 40)
	if w, ok := QuotaBlockWindowFor(reading, "agy", "gemini-4-argon-high", until); !ok || w.ID != "gemini_argon_weekly" {
		t.Errorf("argon: %+v, %v; want the argon family, not gemini_weekly or gemini_5h", w, ok)
	}
	// Flash is served by gemini_weekly and gemini_5h. The run reset at the weekly
	// reset, so the weekly window is the one that ran out.
	if w, ok := QuotaBlockWindowFor(reading, "agy", "gemini-3.8-flash-low", until); !ok || w.ID != "gemini_weekly" {
		t.Errorf("flash: %+v, %v; want gemini_weekly", w, ok)
	}
	if w, ok := QuotaBlockWindowFor(reading, "agy", "claude-opus-5-5-high", until); !ok || w.ID != "claude_gpt_weekly" {
		t.Errorf("claude: %+v, %v; want claude_gpt_weekly", w, ok)
	}
	if _, ok := QuotaBlockWindowFor(nil, "agy", "gemini-4-argon-high", until); ok {
		t.Error("window without a reading")
	}
}

// A block ends when a newer usable reading shows its window with allowance
// left. A reading from before the run's report, a stale reading, or an
// exhausted window does not end it. A provider block is never ended by a reading.
func TestFresherReadingEndsOnlyAWindowBlock(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 20, 0, 0, time.UTC)
	until := now.Add(160 * time.Hour)
	block := QuotaBlock{Provider: "agy", WindowID: "gemini_argon_weekly", WindowLabel: "Gemini Argon weekly", Until: until, Recorded: now}
	whole := QuotaBlock{Provider: "agy", Until: until, Recorded: now}
	argonRefused := func(q *Quotas) bool { return QuotaRefusal(q, "agy", "gemini-4-argon-high") != "" }
	fresh := now.Add(5 * time.Minute)
	open := agyBlockReading(fresh, until, 40)
	for name, tc := range map[string]struct {
		q      *Quotas
		blocks []QuotaBlock
		want   bool
	}{
		"fresher and open ends the window block":   {open, []QuotaBlock{block}, false},
		"an older reading keeps it":                {agyBlockReading(now.Add(-time.Minute), until, 40), []QuotaBlock{block}, true},
		"a stale fresher reading keeps it":         {staleQuotas(open), []QuotaBlock{block}, true},
		"a fresher exhausted window keeps it":      {agyBlockReading(fresh, until, 100), []QuotaBlock{block}, true},
		"no reading time keeps it":                 {&Quotas{Providers: open.Providers}, []QuotaBlock{block}, true},
		"a provider block is not ended by reading": {open, []QuotaBlock{whole}, true},
	} {
		q := WithQuotaBlocks(tc.q, tc.blocks, fresh)
		if got := argonRefused(q); got != tc.want {
			t.Errorf("%s: argon refused %v, want %v", name, got, tc.want)
		}
	}
	// The block still ends at its reset time.
	if r := QuotaRefusal(WithQuotaBlocks(open, []QuotaBlock{block}, until), "agy", "gemini-4-argon-high"); r != "" {
		t.Errorf("refused at the reset: %q", r)
	}
}

// staleQuotas is a copy of q whose agy reading quota-axi marks stale.
func staleQuotas(q *Quotas) *Quotas {
	out := &Quotas{GeneratedAt: q.GeneratedAt}
	for _, p := range q.Providers {
		p.Stale = true
		out.Providers = append(out.Providers, p)
	}
	return out
}
