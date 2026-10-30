package protocol

import "testing"

func TestWorkerProviderAllowed(t *testing.T) {
	q := &Quotas{Providers: []QuotaProvider{{Provider: "deepseek", Windows: []QuotaWindow{{ID: "week"}}}}}
	for _, tt := range []struct {
		h, m    string
		setting []string
		want    bool
	}{
		{"codex", "gpt-6", nil, true}, {"claude", "opus", nil, true}, {"opencode", "openai/gpt-6", nil, true}, {"pi", "opencode-go/model", nil, true}, {"opencode", "deepseek/model", nil, true}, {"opencode", "openrouter/anthropic/claude-opus-5.5", nil, false}, {"pi", "other/model", nil, false},
		{"opencode", "openrouter/model", []string{"openrouter"}, true}, {"codex", "gpt-6", []string{"openrouter"}, false},
	} {
		if got := WorkerProviderAllowed(tt.h, tt.m, tt.setting, q); got != tt.want {
			t.Errorf("%s %s %v: got %v", tt.h, tt.m, tt.setting, got)
		}
	}
}
