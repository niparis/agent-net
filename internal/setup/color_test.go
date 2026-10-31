package setup

import (
	"strings"
	"testing"
)

func TestSetupColorRolesUseBasicANSI(t *testing.T) {
	c := setupColor{enabled: true}
	cases := []struct {
		got, want string
	}{
		{c.good("ok"), "\x1b[32;1mok\x1b[0m"},
		{c.label("See"), "\x1b[36mSee\x1b[0m"},
		{c.attention("!"), "\x1b[33m!\x1b[0m"},
		{c.failure("failed"), "\x1b[31mfailed\x1b[0m"},
		{c.faint("details"), "\x1b[2mdetails\x1b[0m"},
		{c.heading("What you have now"), "\x1b[1mWhat you have now\x1b[0m"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
		if strings.Contains(tc.got, "38;") || strings.Contains(tc.got, "48;") || strings.Contains(tc.got, ";5m") {
			t.Errorf("non-basic ANSI color in %q", tc.got)
		}
	}
}

func TestSetupColorDisabledByEnvironmentOrPipe(t *testing.T) {
	cases := []struct {
		name    string
		noColor string
		term    string
		tty     bool
	}{
		{name: "NO_COLOR", noColor: "1", term: "xterm-256color", tty: true},
		{name: "dumb terminal", term: "dumb", tty: true},
		{name: "pipe", term: "xterm-256color", tty: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColor)
			t.Setenv("TERM", tc.term)
			c := setupColor{enabled: setupColorEnabled(tc.tty)}
			if got := c.label("[Y/n]"); strings.Contains(got, "\x1b") {
				t.Fatalf("disabled color emitted escapes: %q", got)
			}
		})
	}
}
