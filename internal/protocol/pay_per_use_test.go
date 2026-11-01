package protocol

import (
	"errors"
	"strings"
	"testing"
)

// The subscription and pay-per-use rule is the one the worker provider defaults
// use: a subscription harness, codex or opencode-go, or a usable quota reading
// is subscription-backed. Everything else is pay-per-use.
func TestSubscriptionProviderDefaults(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{{Provider: "deepseek", Windows: []QuotaWindow{{ID: "week"}}}}}
	for _, tt := range []struct {
		harness, model string
		want           bool
	}{
		{"codex", "gpt-6.1-sol", true},
		{"claude", "sonnet", true},
		{"pi", "openai-codex/gpt-6.1-sol", true},
		{"opencode", "opencode-go/gpt-6.1-sol", true},
		{"opencode", "deepseek/model", true},
		{"opencode", "opencode/gpt-6.1-sol", false},
		{"opencode", "openrouter/anthropic/claude-opus-5.5", false},
		{"opencode", "ollama-cloud/deepseek-v4.1-flash", false},
	} {
		if got := SubscriptionProvider(tt.harness, tt.model, q); got != tt.want {
			t.Errorf("SubscriptionProvider(%q, %q) = %v, want %v", tt.harness, tt.model, got, tt.want)
		}
		if got := PayPerUseProvider(tt.harness, tt.model, q); got == tt.want {
			t.Errorf("PayPerUseProvider(%q, %q) = %v, want %v", tt.harness, tt.model, got, !tt.want)
		}
	}
}

// A loose name never resolves to a pay-per-use provider, even when it is the
// only match. The error names the full id to write instead.
func TestResolveWorkerModelRefusesLoosePayPerUse(t *testing.T) {
	models := []string{"opencode/gpt-6.1-sol"}
	_, err := ResolveWorkerModelRankedQuota("opencode", "gpt-6.1-sol", models, nil, nil, nil, nil, nil)
	var pu *PayPerUseModelError
	if !errors.As(err, &pu) {
		t.Fatalf("loose name on a pay-per-use provider: %v", err)
	}
	if !strings.Contains(err.Error(), "pay-per-use") || !strings.Contains(err.Error(), "opencode/gpt-6.1-sol") {
		t.Errorf("error does not name the full id: %v", err)
	}
	// The full provider id is accepted.
	got, err := ResolveWorkerModelRankedQuota("opencode", "opencode/gpt-6.1-sol", models, nil, nil, nil, nil, nil)
	if err != nil || got.Model != "opencode/gpt-6.1-sol" {
		t.Fatalf("full id: %+v, %v", got, err)
	}
	// A bare id that happens to be the whole listed name is still not the
	// provider prefix the pay-per-use rule asks for.
	_, err = ResolveWorkerModelRankedQuota("opencode", "gpt-6.1-sol", []string{"gpt-6.1-sol"}, nil, nil, nil, nil, nil)
	if !errors.As(err, &pu) {
		t.Fatalf("bare listed id on a pay-per-use provider: %v", err)
	}
}

// When a loose name matches both a subscription provider and a pay-per-use one,
// the subscription provider wins. A bare name on the Codex harness is a full id
// for Codex, so it resolves there.
func TestResolveWorkerModelPrefersSubscription(t *testing.T) {
	models := []string{"opencode/gpt-6.1-sol", "opencode-go/gpt-6.1-sol"}
	got, err := ResolveWorkerModelRankedQuota("opencode", "gpt-6.1-sol", models, nil, nil, nil, nil, nil)
	if err != nil || got.Model != "opencode-go/gpt-6.1-sol" {
		t.Fatalf("preference: %+v, %v", got, err)
	}
	got, err = ResolveWorkerModelRankedQuota("codex", "gpt-6.1-sol:medium", []string{"gpt-6.1-sol"}, nil, nil, nil, nil, nil)
	if err != nil || got.Model != "gpt-6.1-sol:medium" {
		t.Fatalf("codex effort: %+v, %v", got, err)
	}
}
