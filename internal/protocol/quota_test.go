package protocol

import (
	"strings"
	"testing"
	"time"
)

func quotaAt(provider string, windows ...QuotaWindow) QuotaProvider {
	return QuotaProvider{Provider: provider, Windows: windows}
}

func window(id, kind string, used float64, pace string) QuotaWindow {
	return QuotaWindow{ID: id, Label: id, Kind: kind, PercentUsed: used, Pace: pace,
		ResetsAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC).Unix()}
}

func TestQuotaProviderMapping(t *testing.T) {
	cases := map[string]string{
		"anthropic": "claude", "claude": "claude",
		"openai": "codex", "codex": "codex", "openai-codex": "codex",
		"opencode-go": "opencode-go", "cursor": "cursor",
		"github-copilot": "copilot", "copilot": "copilot",
		"xai": "grok", "kimi": "kimi", "zai": "zai", "z-ai": "zai",
		"google": "agy", "gemini": "agy", "qwen": "alibaba", "xiaomi": "mimo",
		"deepseek": "deepseek", "openrouter": "openrouter", "commandcode": "commandcode",
		"nobody": "",
	}
	for in, want := range cases {
		if got := QuotaProviderForModelProvider(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
	if got := QuotaProviderForStart("claude", "claude-opus-5-5"); got != "claude" {
		t.Errorf("claude harness: %q", got)
	}
	if got := QuotaProviderForStart("codex", "gpt-6"); got != "codex" {
		t.Errorf("codex harness: %q", got)
	}
	if got := QuotaProviderForStart("opencode", "deepseek/deepseek-v4.1"); got != "deepseek" {
		t.Errorf("model prefix: %q", got)
	}
}

func TestQuotaSummary(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{
		quotaAt("cursor", window("included_usage", "monthly", 100, "")),
		quotaAt("codex", window("weekly", "weekly", 90, QuotaPaceAhead)),
		quotaAt("claude", window("seven_day", "weekly", 59, QuotaPaceOnPace)),
		quotaAt("opencode-go", window("weekly", "weekly", 76, QuotaPaceBehind)),
		quotaAt("empty"),
	}}
	got := QuotaSummary(q)
	want := "claude 59%w · codex 90%w · opencode-go 76%w · cursor exhausted"
	if got != want {
		t.Fatalf("QuotaSummary:\n got %q\nwant %q", got, want)
	}
	if QuotaSummary(nil) != "" {
		t.Error("nil quotas gave a line")
	}
}

func TestQuotaStaleIsShownButNotUsed(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{{Provider: "codex", Stale: true,
		Windows: []QuotaWindow{window("weekly", "weekly", 100, "")}}}}
	if _, ok := q.ExhaustedWindow("codex"); ok {
		t.Error("a stale exhaustion stopped a start")
	}
	if !strings.Contains(QuotaSummary(q), "codex exhausted") {
		t.Errorf("stale provider missing from the line: %q", QuotaSummary(q))
	}
}

func TestResolveModelQuotaSkipsAHighProvider(t *testing.T) {
	models := []string{"openai/flash", "opencode-go/flash"}
	q := &Quotas{Providers: []QuotaProvider{
		quotaAt("codex", window("weekly", "weekly", 96, QuotaPaceOnPace)),
		quotaAt("opencode-go", window("weekly", "weekly", 20, QuotaPaceOnPace)),
	}}
	got, err := ResolveModelQuota("flash", models, nil, nil, nil, q)
	if err != nil || got.Model != "opencode-go/flash" {
		t.Fatalf("high provider was not skipped: %+v %v", got, err)
	}
	q.Providers[1].Windows[0].PercentUsed = 100
	q.Providers[1].Windows[0].Exhausted = true
	if _, err = ResolveModelQuota("flash", models, nil, nil, nil, q); err == nil ||
		!strings.Contains(err.Error(), "out of quota") || !strings.Contains(err.Error(), "2026-11-02") {
		t.Fatalf("all exhausted: %v", err)
	}
}

func TestResolveModelQuotaExactStillWorksWithAWarning(t *testing.T) {
	models := []string{"openai/gpt-6", "opencode-go/deepseek-v4.1-flash"}
	q := &Quotas{Providers: []QuotaProvider{
		quotaAt("codex", window("weekly", "weekly", 100, "")),
		quotaAt("opencode-go", window("weekly", "weekly", 20, "")),
	}}
	got, err := ResolveModelQuota("openai/gpt-6", models, nil, nil, nil, q)
	if err != nil || got.Model != "openai/gpt-6" || !got.Exact {
		t.Fatalf("exact id: %+v %v", got, err)
	}
	if !strings.Contains(got.QuotaNote, "codex is exhausted") || !strings.Contains(got.QuotaNote, "resets at") {
		t.Errorf("quota note %q", got.QuotaNote)
	}
	if _, err := ResolveModelQuota("opencode-go/deepseek-v4.1-flash", models, nil, nil, nil, q); err != nil {
		t.Errorf("other exact id: %v", err)
	}
}

func TestResolveModelQuotaRanksAheadLower(t *testing.T) {
	models := []string{"openai/gpt-6", "opencode-go/gpt-6"}
	q := &Quotas{Providers: []QuotaProvider{
		quotaAt("codex", window("weekly", "weekly", 40, QuotaPaceAhead)),
		quotaAt("opencode-go", window("weekly", "weekly", 40, QuotaPaceOnPace)),
	}}
	got, err := ResolveModelQuota("gpt 6", models, nil, nil, nil, q)
	if err != nil || got.Model != "opencode-go/gpt-6" {
		t.Fatalf("ahead provider did not rank lower: %+v %v", got, err)
	}
}

func TestQuotaRefusal(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{
		quotaAt("claude", window("seven_day", "weekly", 100, "")),
	}}
	got := QuotaRefusal(q, "claude", "claude-opus-5-5")
	if !strings.Contains(got, "claude is exhausted") || !strings.Contains(got, "resets at") {
		t.Fatalf("refusal %q", got)
	}
	if QuotaRefusal(nil, "claude", "x") != "" {
		t.Error("nil quotas refused a start")
	}
	q.Providers[0].Windows[0].PercentUsed = 95
	q.Providers[0].Windows[0].Exhausted = false
	if QuotaRefusal(q, "claude", "x") != "" {
		t.Error("95 percent refused a start")
	}
}
