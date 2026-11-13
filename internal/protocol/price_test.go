package protocol

import (
	"math"
	"strings"
	"testing"
)

// fakePrices is a small OpenRouter list: the prices fetched on 2026-11-13 for
// the models the picker uses, plus the entries the lookup must skip.
func fakePrices() *PriceBook {
	return NewPriceBook([]ModelPrice{
		{ID: "anthropic/claude-opus-5.5", Prompt: 4e-6, Completion: 2e-5, CacheRead: 2e-7, CacheWrite: 5e-6},
		{ID: "anthropic/claude-opus-5.5:batch", Prompt: 1e-9, Completion: 1e-9},
		{ID: "~anthropic/claude-opus-latest", Prompt: 1e-9, Completion: 1e-9},
		{ID: "anthropic/claude-sonnet-5.5", Prompt: 2e-6, Completion: 1e-5, CacheRead: 1e-7, CacheWrite: 2.5e-6},
		{ID: "anthropic/claude-haiku-5.5", Prompt: 1e-7, Completion: 5e-7, CacheRead: 1e-8, CacheWrite: 1.25e-7},
		{ID: "deepseek/deepseek-v4.1-flash-1017", Prompt: 3e-7, Completion: 1.2e-6, CacheRead: 6e-9},
		{ID: "openai/gpt-6.1-terra", Prompt: 1e-6, Completion: 8e-6},
		{ID: "openai/gpt-6.1-sol", Prompt: 3e-6, Completion: 1.2e-5},
		{ID: "openai/gpt-6-luna", Prompt: 5e-7, Completion: 4e-6},
		{ID: "google/gemini-4-argon", Prompt: 1.5e-6, Completion: 9e-6},
	})
}

func TestPriceLookupFindsTheIdsThePickerUses(t *testing.T) {
	book := fakePrices()
	for model, want := range map[string]string{
		"claude-opus-5-5:high":                             "anthropic/claude-opus-5.5",
		"claude-sonnet-5-5":                                "anthropic/claude-sonnet-5.5",
		"claude-haiku-5-5:medium":                          "anthropic/claude-haiku-5.5",
		"alibaba-token-plan/deepseek-v4.1-flash-1017:high": "deepseek/deepseek-v4.1-flash-1017",
		"gpt-6.1-terra:default":                            "openai/gpt-6.1-terra",
		"gpt-6.1-sol:high":                                 "openai/gpt-6.1-sol",
		"gpt-6-luna:low":                                   "openai/gpt-6-luna",
		"gemini-4-argon":                                   "google/gemini-4-argon",
		"anthropic/claude-haiku-5-5-20251001":              "anthropic/claude-haiku-5.5",
	} {
		got, ok := book.Lookup(model)
		if !ok || got.ID != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", model, got.ID, ok, want)
		}
	}
}

func TestPriceUnknownModelUsesTheMiddlePrice(t *testing.T) {
	book := fakePrices()
	if _, ok := book.Lookup("mystery-model-9"); ok {
		t.Fatal("an unlisted model must not match")
	}
	p, known, ok := book.PriceOf("mystery-model-9")
	if known || !ok {
		t.Fatalf("PriceOf = known %v ok %v, want unknown with a fallback", known, ok)
	}
	mid, _ := book.Middle()
	if p.ID != mid.ID || p.ID == "" {
		t.Fatalf("fallback %q, want the middle price %q", p.ID, mid.ID)
	}
	if _, _, ok := NewPriceBook(nil).PriceOf("x"); ok {
		t.Fatal("an empty book has no price at all")
	}
}

func TestModelPriceCostPricesEachKind(t *testing.T) {
	opus, _ := fakePrices().Lookup("claude-opus-5-5")
	got := opus.Cost(RunTokens{In: 1000, Out: 100, Reasoning: 50, CacheRead: 100000, CacheWrite: 2000})
	want := 1000*4e-6 + 150*2e-5 + 100000*2e-7 + 2000*5e-6
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("Cost = %v, want %v", got, want)
	}
	// DeepSeek lists no cache write price: the prompt price stands in.
	ds, _ := fakePrices().Lookup("deepseek-v4.1-flash-1017")
	if got, want := ds.Cost(RunTokens{CacheWrite: 1000}), 1000*3e-7; math.Abs(got-want) > 1e-12 {
		t.Fatalf("missing cache write price: Cost = %v, want %v", got, want)
	}
}

// The notice carries the cost at API prices next to the tokens, and keeps the
// cost the harness reports on its own line.
func TestWorkerNoticeShowsTheCostAtAPIPrices(t *testing.T) {
	n := WorkerNotice{Status: StatusDone, Session: "s_1", Harness: "opencode", Model: "m", Project: "p", Host: "h",
		Tokens: "1000 in, 100 out", Cost: "0.0500 (as OpenCode reports it)", APICost: APICostText(0.4321, true)}
	out := FormatWorkerNotice(n, "done")
	if !strings.Contains(out, "tokens: 1000 in, 100 out\n") || !strings.Contains(out, "cost: 0.0500 (as OpenCode reports it)\ncost: $0.43 at API prices\n") {
		t.Errorf("notice:\n%s", out)
	}
	if got := APICostText(0.0004, false); got != "<$0.01 at API prices (price unknown, middle price used)" {
		t.Errorf("small cost with no listed price: %q", got)
	}
	n.APICost = ""
	if out := FormatWorkerNotice(n, "done"); strings.Contains(out, "API prices") {
		t.Errorf("a notice with no cost names API prices:\n%s", out)
	}
}
