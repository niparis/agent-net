package protocol

import (
	"strings"
	"testing"
)

func TestCodexBriefSaysAgentNetToolsAreOff(t *testing.T) {
	for _, want := range []string{"agent-net tools are off in this worker on purpose", "Ignore any instruction", "agent_announce", "final answer at the end of your turn is your report"} {
		if !strings.Contains(WorkerPreambleCodex, want) {
			t.Errorf("the Codex brief lacks %q", want)
		}
	}
	if strings.Contains(WorkerPreambleCodex, "agent_handback") {
		t.Error("the Codex brief names agent_handback")
	}
}
