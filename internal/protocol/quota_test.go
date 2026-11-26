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

func TestQuotaStaleIsNotUsed(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{{Provider: "codex", Stale: true,
		Windows: []QuotaWindow{window("weekly", "weekly", 100, "")}}}}
	if _, ok := q.ExhaustedWindow("codex"); ok {
		t.Error("a stale exhaustion stopped a start")
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
