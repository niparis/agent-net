package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func fmtCostName(w, h int) string { return fmt.Sprintf("tools_cost_list_%dx%d", w, h) }

func costModel(w, h int) Model {
	m, _ := newToolsModel(w, h)
	m = openTools(m)
	for i := range m.tl.items {
		m.tl.items[i].Description = "Description for " + m.tl.items[i].Name
		m.tl.items[i].StartTokens = int64(60 + (i%7)*35) // realistic: a name and a short description
		m.tl.items[i].FullTokens = 3400
	}
	m.rebuildTools()
	return m
}

func TestToolsCostPanelAndProjects(t *testing.T) {
	m := costModel(120, 34)
	r, _ := m.selectedRow()
	if s := plain(m); !strings.Contains(s, "Description for "+r.Name) || !strings.Contains(s, "tokens in every session") || !strings.Contains(s, "when the skill is used") {
		t.Fatalf("missing panel: %s", s)
	}
	m = pressKey(m, "j")
	r, _ = m.selectedRow()
	if !strings.Contains(plain(m), "Description for "+r.Name) {
		t.Fatal("selection did not update panel")
	}
	m = toolFilter(m, "project:homelab")
	if !strings.Contains(plain(m), "skills · ~") {
		t.Fatal("missing project total")
	}
	m = pressKey(m, "t")
	if !strings.Contains(plain(m), "BIGGEST") {
		t.Fatal("missing project table")
	}
	projects := m.projectCosts()
	for i := 1; i < len(projects); i++ {
		if projects[i-1].cost < projects[i].cost {
			t.Fatal("projects not sorted by cost")
		}
	}
	m = pressKey(m, "j")
	selected := projects[m.tl.projectRow].name
	m = pressKey(m, "enter")
	if m.tl.search != "project:"+selected {
		t.Fatal("enter did not filter project")
	}
}

func TestToolsCostGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m := costModel(size[0], size[1])
		golden(t, fmtCostName(size[0], size[1]), m.testView())
		if size[0] == 120 {
			m = pressKey(m, "t")
			golden(t, "tools_cost_projects_120x34", m.testView())
		}
	}
}

func TestProjectCostIncludesOnlyAvailableProjectSkills(t *testing.T) {
	m := costModel(120, 34)
	m.tl.search = ""
	n, total, biggest, _ := m.projectCost("homelab")
	if n != 34 || total != 5610 || biggest != "found-skill-04" {
		t.Fatalf("homelab: %d %d %s", n, total, biggest)
	}
	m = toolFilter(m, "project:homelab")
	if !strings.Contains(plain(m), "34 skills · ~5.6k tokens in every session on research") {
		t.Fatal(plain(m))
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
	m = toolFilter(m, "project:homelab")
	if s := plain(m); !strings.Contains(s, "tokens in every session on Nicolas-MBP") {
		t.Fatalf("highest machine missing: %s", s)
	}
	m = pressKey(m, "t")
	if s := plain(m); !strings.Contains(s, "MACHINE") || !strings.Contains(s, "Nicolas-MBP") {
		t.Fatalf("machine column missing: %s", s)
	}
	for _, r := range m.projectCosts() {
		if r.name == "unknown" && (r.cost != 0 || r.skills != 0) {
			t.Fatalf("unknown machine has a total: %+v", r)
		}
	}
}
