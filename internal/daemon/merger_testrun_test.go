package daemon

import (
	"strings"
	"testing"
)

// Every merger on any machine runs local test suites through the machine's
// admission, so the rule sits in the first message rather than in one
// session's own wrapper.
func TestMergerFirstMessageTestRunRule(t *testing.T) {
	for _, rule := range []string{"agent-net test-run", "test slot", "parallelism"} {
		if !strings.Contains(MergerFirstMessage, rule) {
			t.Errorf("MergerFirstMessage lacks %q", rule)
		}
	}
}
