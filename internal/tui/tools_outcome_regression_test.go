package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestToolOutcomeCountsFailedTargets(t *testing.T) {
	res := protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{State: "ok"}, {State: "failed", Reason: "cannot lock Git index"}, {State: "conflict"}, {State: "pending"}}}
	head := outcomeLines("demo", res)[0]
	if !strings.Contains(head, "2 failed") || strings.Contains(head, "✓") {
		t.Fatalf("failure summary: %s", head)
	}
}
