package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestProtectedChooser(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "anthropic/claude-sonnet"}, {Harness: "opencode", Model: "opencode-go/luna"}}
	scores := []protocol.ModelScore{{Match: "anthropic/claude-sonnet", Score: 55}, {Match: "opencode-go/luna", Score: 45}}
	for _, level := range []string{"normal", "hard"} {
		c, note, err := chooseProtectedWorker(candidates, level, scores, nil, nil, nil, 0, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"})
		if err != nil || c.Model != "opencode-go/luna" || !strings.Contains(note, "skipped") {
			t.Fatalf("%s: %+v %q %v", level, c, note, err)
		}
	}
	scores[1].Score = 10
	c, note, err := chooseProtectedWorker(candidates, "hard", scores, nil, nil, nil, 0, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "orchestrator"})
	if err != nil || c.Model != "anthropic/claude-sonnet" || !strings.Contains(note, "orchestrator") {
		t.Fatalf("fallback: %+v %q %v", c, note, err)
	}
}
