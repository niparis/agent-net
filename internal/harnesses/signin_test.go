package harnesses

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestSignInTimeoutUnknown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := Options{Run: func(ctx context.Context, _ string, _ ...string) (string, error) { return "Not logged in", ctx.Err() }}
	got, _ := signIn(ctx, o, "codex", "fake")
	if got != "" {
		t.Fatal(got)
	}
}

func TestSignInFakeCommands(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
		code               int
	}{
		{"claude", `{"loggedIn":true,"authMethod":"claude.ai"}`, "yes", 0},
		{"claude", `{"loggedIn":false}`, "no", 0},
		{"codex", "Logged in using ChatGPT", "yes", 0},
		{"codex", "Not logged in", "no", 1},
		{"codex", "broken", "", 1},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, tc.name)
		script := "#!/bin/sh\nprintf '%s\\n' '" + tc.output + "' >&2\nexit 0\n"
		if tc.name == "claude" {
			script = "#!/bin/sh\nprintf '%s\\n' '" + tc.output + "'\nexit 0\n"
		}
		if tc.code == 1 {
			script = strings.Replace(script, "exit 0", "exit 1", 1)
		}
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", dir)
		t.Setenv("CODEX_HOME", dir)
		o := Options{Run: func(ctx context.Context, p string, args ...string) (string, error) {
			return runProgram(ctx, p, "/bin", args...)
		}}
		got, _ := signIn(context.Background(), o, tc.name, path)
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
