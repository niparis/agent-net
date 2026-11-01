package setup

import (
	"strings"
	"testing"
)

// The owner decided not to ask about protecting a provider at setup: it
// confuses people before they understand workers. The TUI teaches it later.
func TestSetupDoesNotProposeProtectingAProvider(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	out := run(t, Options{Join: f.newJoin(t)})
	for _, old := range []string{"workers protect", "protect anthropic", "orchestrator's Anthropic subscription"} {
		if strings.Contains(out, old) {
			t.Errorf("setup still teaches the protect command (%q):\n%s", old, out)
		}
	}
}
