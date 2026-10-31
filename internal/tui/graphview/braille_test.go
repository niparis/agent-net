package graphview

import (
	"strings"
	"testing"
)

func TestBrailleGraphShowsNodesAndWildcard(t *testing.T) {
	g := New(fixture())
	out := strings.Join((Braille{}).Render(g, g.Fit(60, 15), 60, 15), "\n")
	for _, want := range []string{"a", "b", "c", "◂all"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "⠁⠂⠄⡀⢀⠈⠐⠠⠃⠆⠤⢄⣀⠉⠒⠚⠶⣶⣿") {
		t.Fatalf("no braille line:\n%s", out)
	}
}
