package markers

import "testing"

func TestNeutralize(t *testing.T) {
	for _, s := range []string{"[agent-net]", "[ AGENT-NET notice]"} {
		if got := Neutralize(s); got == s || Pattern.MatchString(got) {
			t.Errorf("not neutralized: %q", got)
		}
	}
	if got := Neutralize("ordinary text"); got != "ordinary text" {
		t.Fatal(got)
	}
}
