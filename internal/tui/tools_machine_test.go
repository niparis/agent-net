package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolsTurnOffOnAMachineCellIsLimitedToThatMachine(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "research", "agent-net")
	m = pressKey(m, "l") // the second machine column is research
	m = pressKey(m, "u")
	if m.tl.modal == nil {
		t.Fatalf("u must open a plan: %q", m.tl.toast)
	}
	if len(src.plans) != 1 || src.plans[0].Target.Scope != "uninstalled" || strings.Join(src.plans[0].Target.Machines, ",") != toolNodeR {
		t.Fatalf("plans %+v", src.plans)
	}
	v := plain(m)
	for _, want := range []string{"Turn off research on research", "only on research", "other machines are not changed"} {
		if !strings.Contains(v, want) {
			t.Errorf("plan text lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Nicolas-MBP:") {
		t.Errorf("the plan lists the machine that was not chosen:\n%s", v)
	}
}

func TestToolsTurnOnOnAMachineCellIsLimitedToThatMachine(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "code-review", "agent-net")
	m = pressKey(m, "h") // the first machine column is Nicolas-MBP, where code-review is another version
	m = pressKey(m, "a")
	if m.tl.modal == nil {
		t.Fatalf("a must open a plan: %q", m.tl.toast)
	}
	if len(src.plans) != 1 || src.plans[0].Target.Scope != "global" || strings.Join(src.plans[0].Target.Machines, ",") != toolNodeM {
		t.Fatalf("plans %+v", src.plans)
	}
	v := plain(m)
	if !strings.Contains(v, "only on Nicolas-MBP") || strings.Contains(v, "research: code-review") {
		t.Errorf("plan text:\n%s", v)
	}
	m = pressKey(m, "enter")
	if len(src.applied) != 1 {
		t.Fatalf("applied %v", src.applied)
	}
}

func TestToolsWithOneMachineHasNoMachineScope(t *testing.T) {
	m, src := newToolsModel(120, 34)
	src.fakeSource.snap.Nodes = src.fakeSource.snap.Nodes[:1]
	src.items = []protocol.ToolItem{toolManaged(toolSkill("solo", "sha256:1212121aaaa", toolLoc(toolNodeR, "research", "/home/n/.claude/skills/solo", "user", "", "claude")), "global")}
	m = refresh(m, src)
	m = openTools(m)
	m = pressKey(m, "]")
	m = rowByNameFrom(m, "solo", "agent-net")
	m = pressKey(m, "u")
	if len(src.plans) != 1 || len(src.plans[0].Target.Machines) != 0 {
		t.Fatalf("plans %+v", src.plans)
	}
	if strings.Contains(plain(m), "only on") {
		t.Errorf("a single machine needs no scope note:\n%s", plain(m))
	}
}
