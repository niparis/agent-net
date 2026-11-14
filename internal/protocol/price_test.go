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
		{ID: "google/gemini-4-argon", Prompt: 2e-6, Completion: 1e-5, CacheRead: 1e-7},
		{ID: "google/gemini-3.8-flash", Prompt: 3e-7, Completion: 2.5e-6, CacheRead: 3e-8},
		// A model whose own name ends in an effort word: the exact entry wins.
		{ID: "openai/rocket-high", Prompt: 8e-6, Completion: 4e-5},
		{ID: "openai/rocket", Prompt: 1e-7, Completion: 1e-7},
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

// Antigravity keeps the reasoning effort in the model id (gemini-4-argon-high),
// so the lookup must try the name without that known suffix too.
func TestPriceLookupFindsAgyModelIds(t *testing.T) {
	book := fakePrices()
	for model, want := range map[string]string{
		"gemini-4-argon-high":      "google/gemini-4-argon",
		"gemini-3.8-flash-medium":  "google/gemini-3.8-flash",
		"claude-sonnet-5-5-medium": "anthropic/claude-sonnet-5.5",
	} {
		got, ok := book.Lookup(model)
		if !ok || got.ID != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", model, got.ID, ok, want)
		}
	}
}

// An effort suffix is only removed when the full name has no listing, so a
// model whose real name ends in "high" keeps its own price.
func TestPriceLookupKeepsARealNameEndingInAnEffortWord(t *testing.T) {
	book := fakePrices()
	got, ok := book.Lookup("rocket-high")
	if !ok || got.ID != "openai/rocket-high" {
		t.Fatalf("Lookup(rocket-high) = %q, %v; want the exact listing", got.ID, ok)
	}
}

// The Argon run reported on 2026-11-14: 2,070,583 in, 454,051 out, 173,174
// reasoning and 39,527,376 cache read tokens, cumulative over two hand-backs.
// OpenRouter lists google/gemini-4-argon at $2/M in, $10/M out and $0.10/M
// cache read. agy reports out without reasoning, so both are priced.
func TestAgyArgonRunCostFromTheReport(t *testing.T) {
	book := fakePrices()
	p, known, ok := book.PriceOf("gemini-4-argon-high")
	if !ok || !known || p.ID != "google/gemini-4-argon" {
		t.Fatalf("PriceOf = %q, known %v, ok %v; want the listed Argon price", p.ID, known, ok)
	}
	cost := p.Cost(RunTokens{In: 2070583, Out: 454051, Reasoning: 173174, CacheRead: 39527376})
	if want := 14.3661536; math.Abs(cost-want) > 1e-9 {
		t.Fatalf("Cost = %v, want %v", cost, want)
	}
	if got := APICostText(cost, known); got != "$14.37 at API prices" {
		t.Fatalf("APICostText = %q", got)
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

// The "tokens:" line of a stored notice reads back into the same counts, for
// every shape tokenText writes.
func TestParseTokenTextReadsTheNoticeLine(t *testing.T) {
	for text, want := range map[string]RunTokens{
		"1200 in, 340 out": {In: 1200, Out: 340},
		"1200 in, 340 out, 50 reasoning, 90 cache read, 5 cache write": {In: 1200, Out: 340, Reasoning: 50, CacheRead: 90, CacheWrite: 5},
		"13 in, 11 out, 100 cache read, 5 cache write":                 {In: 13, Out: 11, CacheRead: 100, CacheWrite: 5},
	} {
		if got, ok := ParseTokenText(text); !ok || got != want {
			t.Errorf("ParseTokenText(%q) = %+v, %v; want %+v", text, got, ok, want)
		}
	}
	if _, ok := ParseTokenText("none"); ok {
		t.Error("text with no count must not parse")
	}
}
