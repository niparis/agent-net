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

func TestProtectedChooserQuotaFallback(t *testing.T) {
	candidates := []protocol.WorkerModelCandidate{{Harness: "opencode", Model: "anthropic/claude-sonnet"}, {Harness: "opencode", Model: "opencode-go/luna"}}
	scores := []protocol.ModelScore{{Match: "claude-sonnet", Score: 55}, {Match: "luna", Score: 45}}
	q := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "opencode-go", Windows: []protocol.QuotaWindow{{Exhausted: true}}}}}
	c, note, err := chooseProtectedWorker(candidates, "hard", scores, nil, q, nil, 0, protocol.ProtectedProviders{Providers: []string{"anthropic"}, Reason: "keep limit"})
	if err != nil || c.Model != candidates[0].Model || !strings.Contains(note, "keep limit") {
		t.Fatalf("fallback: %+v %q %v", c, note, err)
	}
}
