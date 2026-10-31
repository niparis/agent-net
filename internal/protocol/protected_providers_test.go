package protocol

import "testing"

func TestProtectedProfileProvider(t *testing.T) {
	for _, tt := range []struct{ harness, model, want string }{
		{"claude", "sonnet", "anthropic"}, {"opencode", "claude-sonnet-4", "anthropic"},
		{"codex", "gpt-5", "openai"}, {"opencode", "openai-codex/gpt-5", "openai-codex"},
		{"pi", "anthropic/claude-sonnet-4", "anthropic"}, {"opencode", "opencode-go/luna", "opencode-go"},
	} {
		if got := ProfileProvider(tt.harness, tt.model); got != tt.want {
			t.Errorf("%s %s: got %q want %q", tt.harness, tt.model, got, tt.want)
		}
	}
}
