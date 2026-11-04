package tui

import (
	"charm.land/lipgloss/v2"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestToolsProjectInfoAndTabs(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	for _, want := range []string{"[ Skills per project ]", "All tools", "Projects", "Project information", "Machines: research", "tab switches"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q", want)
		}
	}
	m = pressKey(m, "right")
	for _, want := range []string{"Skill information", "on in 13 of 13 projects", "New projects: on"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestToolsProjectTogglePreviewAndCancel(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if m.tl.modal == nil || !strings.Contains(plain(m), "Switch off archify in homelab.") {
		t.Fatal("space must show the gateway plan: " + plain(m))
	}
	if len(s.applied) != 0 || len(s.imports) != 0 {
		t.Fatal("preview changed files")
	}
	m = pressKey(m, "esc")
	if len(s.applied) != 0 || len(s.imports) != 0 {
		t.Fatal("cancel changed files")
	}
}

func TestToolsProjectEnterToggles(t *testing.T) {
	m, s := agentToolsFixture(80, 24)
	m = pressKey(m, "right")
	m = pressKey(m, "enter")
	if m.tl.modal == nil || len(s.toggles) != 1 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatal("Enter must preview a project toggle")
	}
}

func TestToolsProjectRefusedPlan(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	s.togglePlan = &protocol.ToolPlan{Conflicts: []string{"project is not writable"}, Targets: []protocol.ToolPlanTarget{{Machine: "research", Conflict: "project is not writable", State: "conflict"}}}
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	text := plain(m)
	if !strings.Contains(text, "Nothing changed:") || strings.Count(text, "project is not writable") != 1 {
		t.Fatal(text)
	}
	m = pressKey(m, "enter")
	if len(s.applied) != 0 {
		t.Fatal("refused plan applied")
	}
}

func TestToolsProjectEmptyApply(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	s.applyResult = &protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{State: "conflict", Reason: "folder changed"}}}
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	m = pressKey(m, "enter")
	text := plain(m)
	if !strings.Contains(text, "Nothing changed: folder changed") || strings.Contains(text, "Applied:") {
		t.Fatal(text)
	}
}

func TestToolsProjectToggleErrorAndSkipped(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	s.toggleErr = errors.New("no writable checkout")
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "archify: nothing changed: no writable checkout") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "esc")
	s.toggleErr = nil
	s.togglePlan = &protocol.ToolPlan{Skipped: []protocol.ToolPlanSkip{{Machine: "research", Project: "homelab", Reason: "destination contains a symlink"}}}
	m = pressKey(m, "enter")
	if !strings.Contains(plain(m), "Skipped, cannot hold project skills: homelab (its skill folder is a symlink)") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "left")
	if !strings.Contains(plain(m), "Cannot hold project skills: research: destination contains a symlink") {
		t.Fatal(plain(m))
	}
	s.togglePlan = &protocol.ToolPlan{ID: "fixed", Targets: []protocol.ToolPlanTarget{{Machine: "research", State: "pending"}}}
	m = pressKey(m, "right")
	m = pressKey(m, "enter")
	m = pressKey(m, "esc")
	m = pressKey(m, "left")
	if strings.Contains(plain(m), "Cannot hold project skills:") {
		t.Fatal("a fresh eligible plan must clear the old checkout refusal")
	}

}

func TestToolsProjectDescriptionAndFocus(t *testing.T) {
	old := testColorProfile
	defer setTestColorProfile(old)
	setTestColorProfile(termenv.TrueColor)
	m, s := agentToolsFixture(80, 24)
	s.items[0].Description = strings.Repeat("A description from front matter. ", 20)
	m = pressKey(m, "5")
	projectView := strings.Join(m.agentToolsView(80, 18), "\n")
	m = pressKey(m, "right")
	skillView := strings.Join(m.agentToolsView(80, 18), "\n")
	if projectView == skillView {
		t.Fatal("focus did not change styles")
	}
	border := lipgloss.NewStyle().Foreground(roleAccent).Render("╭─")
	var projectHeading, skillHeading, projectRow string
	for _, line := range m.agentToolsView(80, 18) {
		if strings.Contains(ansi.Strip(line), "╭─Projects") {
			projectHeading = line
		}
		if strings.Contains(ansi.Strip(line), "▸ homelab") {
			projectRow = line
		}
	}
	for _, line := range strings.Split(projectView, "\n") {
		if strings.Contains(ansi.Strip(line), "╭─Projects") {
			skillHeading = line
		}
	}
	if strings.HasPrefix(projectHeading, border) || !strings.HasPrefix(skillHeading, border) {
		t.Fatal("project focus border must match Sessions accent")
	}
	if !strings.Contains(projectHeading, border) {
		t.Fatal("focused skills border must be accented")
	}
	sample := selectedRow("selected")
	prefix := sample[:strings.Index(sample, "selected")]
	if strings.Contains(projectRow, tsAcc.Render("▸")) {
		t.Fatal("inactive project marker must be dim")
	}
	if prefix == "" || !strings.Contains(projectRow, prefix) {
		t.Fatal("inactive project selection lost its Sessions background")
	}
	info := m.agentInfoView(80)
	descLines := 0
	for _, line := range info {
		if strings.Contains(ansi.Strip(line), "description") {
			descLines++
		}
	}
	if descLines != 2 {
		t.Fatalf("description must use two lines, got %d", descLines)
	}
}

func TestToolsProjectSkillPaneUsesAvailableRows(t *testing.T) {
	m, _ := agentToolsFixture(80, 24)
	m = pressKey(m, "right")
	if !strings.Contains(plain(m), "[ ] react-best-practices") {
		t.Fatal("the four skill rows must fit above the information box")
	}
}

func TestToolsProjectsExcludeSeenFoldersWithoutGitRoot(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	for _, name := range []string{"backend", "home-one", "home-two"} {
		m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: name, NodeID: toolNodeR, Path: "/tmp/" + name, Source: protocol.SourceSeen})
		m.snap.Agents = append(m.snap.Agents, protocol.AgentView{SessionID: "seen-" + name, NodeID: toolNodeR, Repo: protocol.RepoInfo{Project: name}})
	}
	s.projectStates = []protocol.ToolProjectState{
		{Projects: map[string]bool{"homelab": true}},
		{Projects: map[string]bool{"tenebrae": false}},
	}
	m = pressKey(m, "5")
	if m.tl.agentRow >= len(m.toolsProjects()) {
		t.Fatal("refresh must keep the selected project within the filtered list")
	}
	if got := strings.Join(m.toolsProjects(), ","); got != "homelab,tenebrae" {
		t.Fatalf("left pane must use gateway Git projects, got %s", got)
	}
	for _, name := range []string{"backend", "home-one", "home-two"} {
		if strings.Contains(plain(m), name) {
			t.Errorf("seen folder without Git root listed: %s", name)
		}
	}
	if n, _, _, _ := m.projectCost("homelab"); n != 2 {
		t.Fatalf("per-project skill count changed: %d", n)
	}
	if !strings.Contains(plain(m), "homelab          2") {
		t.Fatal("left pane lost the project skill count: " + plain(m))
	}
}
