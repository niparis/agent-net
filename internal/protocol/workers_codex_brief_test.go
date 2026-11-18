package protocol

import (
	"regexp"
	"strings"
	"testing"
)

func TestCodexBriefSaysAgentNetToolsAreOff(t *testing.T) {
	for _, want := range []string{"agent-net tools are off in this worker on purpose", "Ignore any instruction", "to call an agent-net tool", "final answer at the end of your turn is your report"} {
		if !strings.Contains(WorkerPreambleCodex, want) {
			t.Errorf("the Codex brief lacks %q", want)
		}
	}
	// A Codex worker has no agent-net tools, so the brief names none.
	if name := regexp.MustCompile(`agent_[a-z_]+`).FindString(WorkerPreambleCodex); name != "" {
		t.Errorf("the Codex brief names %s, a tool the worker does not have", name)
	}
}
