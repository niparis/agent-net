package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestComparePathShortening(t *testing.T) {
	got := comparePath("/home/test/.pi/agent/skills/demo", 20)
	if got != "…/agent/skills/demo" {
		t.Fatalf("got %q", got)
	}
	if got := comparePath("/a/verylongskillname", 8); got != "…" {
		t.Fatalf("oversized name: %q", got)
	}
}

func TestProjectOutcomeHasOneName(t *testing.T) {
	got := projectOutcomeLines("archify", protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{State: "ok"}}})[0]
	if strings.Count(got, "archify") != 1 {
		t.Fatalf("got %q", got)
	}
}
