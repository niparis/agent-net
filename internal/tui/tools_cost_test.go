package tui

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func costModel(w, h int) Model {
	m, _ := newToolsModel(w, h)
	m = openTable(m)
	for i := range m.tl.items {
		m.tl.items[i].Description = "Description for " + m.tl.items[i].Name
		m.tl.items[i].StartTokens = int64(60 + (i%7)*35) // realistic: a name and a short description
		m.tl.items[i].FullTokens = 3400
	}
	m.rebuildTools()
	return m
}

// The cost line under the table names the selected project, its skill count
// and its token cost per session.
func TestTableCostLine(t *testing.T) {
	m := costModel(120, 34)
	col := -1
	for i, p := range m.tl.projectCols {
		if p == "homelab" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("homelab column missing: %v", m.tl.projectCols)
	}
	m.tl.col, m.tl.costCol = col, col
	v := plain(m)
	if !strings.Contains(v, "homelab loads") || !strings.Contains(v, "tokens per session") {
		t.Fatalf("cost line missing:\n%s", v)
	}
	if strings.Contains(v, "MACHINE") || strings.Contains(v, "BIGGEST") {
		t.Fatalf("the old project cost table is back:\n%s", v)
	}
}

func TestProjectCostIncludesOnlyAvailableProjectSkills(t *testing.T) {
	m := costModel(120, 34)
	m.tl.search = ""
	n, total, biggest, _ := m.projectCost("homelab")
	if n != 34 || total != 5610 || biggest != "found-skill-04" {
		t.Fatalf("homelab: %d %d %s", n, total, biggest)
	}
}

func TestProjectCostChoosesHighestMachineAndUnknown(t *testing.T) {
	m := costModel(120, 34)
	m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "homelab", NodeID: toolNodeM, Hostname: "Nicolas-MBP"}, protocol.ProjectView{Name: "unknown"})
	// Add a large user-level skill only on the second machine.
	skill := toolSkill("mac-only", "mac", toolLoc(toolNodeM, "Nicolas-MBP", "/skills/mac-only", "user", "", "claude"))
	skill.StartTokens = 100000
	m.tl.items = append(m.tl.items, skill)
	m.rebuildTools()
	_, _, _, machine := m.projectCost("homelab")
	if machine != "Nicolas-MBP" {
		t.Fatalf("highest machine: %s", machine)
	}
	for _, r := range m.projectCosts() {
		if r.name == "unknown" && (r.cost != 0 || r.skills != 0) {
			t.Fatalf("unknown machine has a total: %+v", r)
		}
	}
}
