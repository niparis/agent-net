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
		for _, want := range []string{"agent_worker_start", `isolation: "none"`, "checklist", "brief for a peer who knows nothing",
			"a definition of done (for example: the new test fails before the fix and passes after)", "how you are graded (for example: the caller reruns the named test",
			"Wait for the worker's notice", "Do not poll", "Check a worker's report before you tell me", "model output",
			"agent_handback", "[agent-net worker brief]", "agent_worker_stop", "defect_found_later", "Report a defect found later", "severity from 1 to 4"} {
			if !strings.Contains(b, want) {
				t.Errorf("variant %s lacks %q", v, want)
			}
		}
	}
}
