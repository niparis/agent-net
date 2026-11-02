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
		{"---\ndescription: " + strings.Repeat("word ", 300) + "\n---", strings.TrimSpace(strings.Repeat("word ", 204))},
	} {
		if got := skillDescription([]byte(c.in)); got != c.want {
			t.Fatalf("got %q want %q", got, c.want)
		}
	}
}
