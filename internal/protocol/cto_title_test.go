package protocol

import (
	"strings"
	"testing"
)

func TestNormalizeCTOTitle(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		bad  string // a fragment of the error message, when the value is refused
	}{
		{name: "plain", raw: "Atlas", want: "Atlas"},
		{name: "trimmed", raw: "  Atlas  ", want: "Atlas"},
		{name: "empty means the default", raw: "   ", want: ""},
		{name: "spaces inside", raw: "The Boss", want: "The Boss"},
		{name: "24 characters", raw: strings.Repeat("a", 24), want: strings.Repeat("a", 24)},
		{name: "25 characters", raw: strings.Repeat("a", 25), bad: "24"},
		{name: "control character", raw: "At\nlas", bad: "printable"},
		{name: "non printable", raw: "At\u200blas", bad: "printable"},
		{name: "agent-net note marker", raw: "Atlas [agent-net", bad: "agent-net"},
		{name: "cross session marker", raw: "Atlas <cross-session", bad: "cross-session"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeCTOTitle(tc.raw)
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.bad) {
					t.Fatalf("NormalizeCTOTitle(%q) = %q, %v; want an error mentioning %q", tc.raw, got, err, tc.bad)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeCTOTitle(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestCTOTitleOrDefault(t *testing.T) {
	if got := CTOTitleOrDefault(""); got != DefaultCTOTitle {
		t.Fatalf("empty title = %q; want %q", got, DefaultCTOTitle)
	}
	if got := CTOTitleOrDefault("Atlas"); got != "Atlas" {
		t.Fatalf("title = %q; want Atlas", got)
	}
}
