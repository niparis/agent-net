package inventory

import (
	"strings"
	"testing"
)

func TestSkillDescription(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"---\ndescription: >\n  hello\n  world\n---\nbody", "hello world"},
		{"---\ndescription: \"hello\\tworld\\u0001\"\n---", "hello world"},
		{"body", ""}, {"---\nname: demo\n---", ""},
		{"---\ndescription: " + strings.Repeat("word ", 300) + "\n---", strings.TrimSpace(strings.Repeat("word ", 205))},
	} {
		if got := skillDescription([]byte(c.in)); got != c.want {
			t.Fatalf("got %q want %q", got, c.want)
		}
	}
}

func TestSkillDescriptionWordBoundary(t *testing.T) {
	for _, text := range []string{strings.Repeat("x", 1025), strings.Repeat("x", 1024) + " tail"} {
		got := skillDescription([]byte("---\ndescription: " + text + "\n---"))
		want := ""
		if len(text) > 1025 {
			want = strings.Repeat("x", 1024)
		}
		if got != want {
			t.Fatalf("got %d characters, want %d", len(got), len(want))
		}
	}
}
