package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// With one machine there is nothing to limit, so the plan names no machine.
func TestToolsWithOneMachineHasNoMachineScope(t *testing.T) {
	m, src := newToolsModel(120, 34)
	src.fakeSource.snap.Nodes = src.fakeSource.snap.Nodes[:1]
	src.items = []protocol.ToolItem{toolManaged(toolSkill("solo", "sha256:1212121aaaa", toolLoc(toolNodeR, "research", "/home/n/.claude/skills/solo", "user", "", "claude")), "global")}
	m = refresh(m, src)
	m = openTools(m)
	m = toolFilter(m, "solo")
	m = pressKey(m, "enter")
	m = toolChoose(t, m, "Turn off for...")
	m = toolPick(t, m, "Machine: research")
	m = pressKey(m, "enter")
	if len(src.plans) != 1 || len(src.plans[0].Target.Machines) != 0 {
		t.Fatalf("plans %+v", src.plans)
	}
	if strings.Contains(plain(m), "only on") {
		t.Errorf("a single machine needs no scope note:\n%s", plain(m))
	}
}
