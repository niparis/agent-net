package protocol

import "testing"

func TestModelDrift(t *testing.T) {
	for _, tc := range []struct{ requested, running, want string }{
		{"claude-sonnet-5-5", "claude-fable-5-2", "asked claude-sonnet-5-5, runs claude-fable-5-2"},
		{"claude-sonnet", "claude-opus-5-5", "asked claude-sonnet, runs claude-opus-5-5"},
		{"sonnet", "claude-sonnet-5-5", ""},
		{"claude-opus-5-5", "claude-opus-5-5[1m]", ""},
		{"anthropic/claude-sonnet-5", "claude-sonnet-5", ""},
		{"gpt-6.1-sol high", "gpt-6.1-sol", ""},
		{"", "claude-fable-5-2", ""},
		{"claude-sonnet-5-5", "", ""},
	} {
		if got := ModelDrift(tc.requested, tc.running); got != tc.want {
			t.Errorf("ModelDrift(%q, %q) = %q, want %q", tc.requested, tc.running, got, tc.want)
		}
	}
}
