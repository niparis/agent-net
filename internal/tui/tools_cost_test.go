package tui

import (
	"fmt"
	"strings"
	"testing"
)

func fmtCostName(w, h int) string { return fmt.Sprintf("tools_cost_list_%dx%d", w, h) }

func costModel(w, h int) Model {
	m, _ := newToolsModel(w, h)
	m = openTools(m)
	for i := range m.tl.items {
		m.tl.items[i].Description = "Description for " + m.tl.items[i].Name
		m.tl.items[i].StartTokens = int64((i + 1) * 100)
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
		golden(t, fmtCostName(size[0], size[1]), m.View())
		if size[0] == 120 {
			m = pressKey(m, "t")
			golden(t, "tools_cost_projects_120x34", m.View())
		}
	}
}

func TestProjectCostIncludesOnlyAvailableProjectSkills(t *testing.T) {
	m := costModel(120, 34)
	m.tl.search = ""
	n, total, biggest := m.projectCost("homelab")
	if n != 7 || total != 5400 || biggest != "scratch-notes" {
		t.Fatalf("homelab: %d %d %s", n, total, biggest)
	}
	m = toolFilter(m, "project:homelab")
	if !strings.Contains(plain(m), "7 skills · ~5.4k tokens in every session") {
		t.Fatal(plain(m))
	}
}
