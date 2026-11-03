package harnesses

import (
	"context"
	"testing"
)

func TestSignInProbe(t *testing.T) {
	for _, tc := range []struct{ name, out, want, method string }{
		{"claude", `{"loggedIn":true,"authMethod":"claude.ai","email":"private@example.invalid"}`, "yes", "claude.ai"},
		{"claude", `{"loggedIn":false}`, "no", ""},
		{"codex", "Logged in using ChatGPT", "yes", "ChatGPT"},
		{"codex", "Not logged in", "no", ""},
	} {
		o := Options{Run: func(context.Context, string, ...string) (string, error) { return tc.out, nil }}
		got, method := signIn(context.Background(), o, tc.name, "fake")
		if got != tc.want || method != tc.method {
			t.Fatalf("%s: %q %q", tc.name, got, method)
		}
	}
}
