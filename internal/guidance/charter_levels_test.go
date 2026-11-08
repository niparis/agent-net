package guidance

import (
	"strings"
	"testing"
)

func TestCharterInheritanceGuidance(t *testing.T) {
	for _, text := range []string{"effective charter", "Inherited refuses always apply", "No charter adds no limit"} {
		if !strings.Contains(Block(Combined), text) {
			t.Fatalf("missing %q", text)
		}
	}
}
