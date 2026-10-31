package toolsdiff

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func onlyOn(it protocol.ToolItem, nodes ...string) protocol.ToolItem {
	it.Desired.Assignment.Machines = nodes
	return it
}

func TestOnOneMachineOnlyIsNotDrift(t *testing.T) {
	// The owner turned v3 on for research only. The Mac keeps its old v1.
	v3 := onlyOn(versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 3), "n_r")
	v1 := versioned(skill("code-review", "sha:bbbbbbbb22", loc("n_m", "mac", "/m/.claude/skills/code-review", "user", "")), 1)
	if d := find(Differences([]protocol.ToolItem{v3, v1}, machines), KindDrift, "code-review"); d != nil {
		t.Fatalf("machine-scoped state reported as drift: %+v", d)
	}
	// Without the machine scope the same copies are drift.
	v3.Desired.Assignment.Machines = nil
	if d := find(Differences([]protocol.ToolItem{v3, v1}, machines), KindDrift, "code-review"); d == nil {
		t.Fatal("drift lost for an assignment on every machine")
	}
}

func TestMachineScopeCellsAndDetail(t *testing.T) {
	v3 := onlyOn(versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/.claude/skills/code-review", "user", "")), "global"), 3), "n_r")
	c := Build([]protocol.ToolItem{v3}, machines)
	var row *Row
	for i := range c.Rows {
		if c.Rows[i].Origin == OriginStore {
			row = &c.Rows[i]
		}
	}
	if row == nil {
		t.Fatal("no store row")
	}
	for i, m := range c.Machines {
		switch m.Name {
		case "research":
			if row.Cells[i].State != CellOn {
				t.Errorf("research cell %+v", row.Cells[i])
			}
		case "mac":
			if row.Cells[i].State != CellOff {
				t.Errorf("mac is not part of the change, so it must not wait: %+v", row.Cells[i])
			}
		}
	}
	if !strings.Contains(row.Detail, "research only") {
		t.Errorf("detail %q", row.Detail)
	}
	// On every machine, the Mac is waiting.
	v3.Desired.Assignment.Machines = nil
	c = Build([]protocol.ToolItem{v3}, machines)
	for _, r := range c.Rows {
		if r.Origin != OriginStore {
			continue
		}
		for i, m := range c.Machines {
			if m.Name == "mac" && r.Cells[i].State != CellPartial {
				t.Errorf("mac cell with an assignment on every machine: %+v", r.Cells[i])
			}
		}
	}
}

func TestUninstallOnOneMachineDetail(t *testing.T) {
	v3 := onlyOn(versioned(withDesired(skill("code-review", "sha:aaaaaaaa11", loc("n_r", "research", "/h/x", "user", "")), "uninstalled"), 3), "n_m")
	c := Build([]protocol.ToolItem{v3}, machines)
	for _, r := range c.Rows {
		if r.Origin == OriginStore && !strings.Contains(r.Detail, "turned off on mac") {
			t.Errorf("detail %q", r.Detail)
		}
	}
}
