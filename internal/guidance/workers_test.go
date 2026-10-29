package guidance

import (
	"strings"
	"testing"
)

// The caller's rules for workers (decision 0002, W14) are in the block that
// goes to every harness.
func TestGuidanceCoversDelegation(t *testing.T) {
	for _, v := range []string{Claude, Other} {
		b := Block(v)
		for _, want := range []string{"agent_start", `isolation: "worktree"`, "brief for a peer who knows nothing",
			"Wait for the worker's notice", "Do not poll", "Check a worker's report before you tell me", "model output",
			"agent_handback", "[agent-net worker brief]", "agent_stop"} {
			if !strings.Contains(b, want) {
				t.Errorf("variant %s lacks %q", v, want)
			}
		}
	}
}
