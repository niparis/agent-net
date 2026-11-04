package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func agentToolsFixture(w, h int) (Model, *fakeTools) {
	m, s := newToolsModel(w, h)
	m.snap.Nodes[1].Hostname = "laptop"
	s.items = []protocol.ToolItem{
		toolManaged(toolSkill("archify", "a"), "global"),
		toolSkill("estate-runbook", "b", toolLoc(toolNodeR, "research", "/src/homelab/.agents/skills/estate-runbook", "project", "homelab", "codex")),
		toolSkill("found-skill", "c", toolLoc(toolNodeM, "laptop", "/tmp/skills/found-skill", "user", "", "codex")),
		toolManaged(toolSkill("react-best-practices", "d"), "projects", "tenebrae"),
	}
	for i := range s.items {
		s.items[i].StartTokens = int64(120 + i*40)
		s.items[i].Description = "Read the project layout and explain its architecture before planning a change. Use the repository conventions and keep the result easy to review."
	}
	m = pressKey(m, "5")
	for i, p := range m.toolsProjects() {
		if p == "homelab" {
			m.tl.agentRow = i
		}
	}
	return m, s
}

func TestToolsAgentsView(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	text := plain(m)
	for _, want := range []string{"Projects", "Skills per project", "[x] archify", "[x] estate-runbook", "[ ] found-skill", "[ ] react-best-practices", "always on", "outside agent-net"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	m = pressKey(m, "tab")
	if !m.tl.allTools || !strings.Contains(plain(m), "STATUS") {
		t.Fatal("tab must open all tools")
	}
	m = pressKey(m, "tab")
	if m.tl.allTools {
		t.Fatal("tab must return to agents")
	}
}

func TestToolsAgentsProjectNamesFitAt80Columns(t *testing.T) {
	m, _ := agentToolsFixture(80, 24)
	m.tl.states = []protocol.ToolProjectState{{Projects: map[string]bool{"flood-warning": false, "homelab-prod": false}}}
	m.tl.agentRow = 0
	text := plain(m)
	for _, project := range []string{"flood-warning", "homelab-prod"} {
		if !strings.Contains(text, project) {
			t.Errorf("project %q was truncated at 80 columns:\n%s", project, text)
		}
	}
}

func TestToolsAgentsPendingPlan(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if m.tl.modal == nil || len(s.toggles) != 1 || len(s.applied) != 0 {
		t.Fatal("toggle must preview before apply")
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if len(m.tl.agentPending["homelab"]) != 1 || !strings.Contains(m.activeToast(), "always on") {
		t.Fatal("repo skill was toggled")
	}
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "Review 2 changes") || len(s.applied) != 0 {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "esc")
	if !strings.Contains(plain(m), "Discard 2 changes? y/n") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "n")
	if len(m.tl.agentPending["homelab"]) != 2 {
		t.Fatal("n discarded changes")
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "y")
	if len(m.tl.agentPending["homelab"]) != 0 {
		t.Fatal("y did not discard")
	}
}

func TestToolsAgentsGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, _ := agentToolsFixture(size[0], size[1])
		golden(t, fmt.Sprintf("tools_agents_%dx%d", size[0], size[1]), m.testView())
		m = pressKey(m, "right")
		golden(t, fmt.Sprintf("tools_agents_skill_%dx%d", size[0], size[1]), m.testView())
		m = pressKey(m, "right")
		m = pressKey(m, " ")
		m = pressKey(m, "esc")
		m = pressKey(m, "down")
		m = pressKey(m, "down")
		m = pressKey(m, " ")
		m = pressKey(m, "esc")
		golden(t, fmt.Sprintf("tools_agents_pending_%dx%d", size[0], size[1]), m.testView())
		for _, line := range strings.Split(plain(m), "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("line too wide: %s", line)
			}
		}
		m = pressKey(m, "enter")
		golden(t, fmt.Sprintf("tools_agents_plan_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsAgentsAssignmentsAndFilter(t *testing.T) {
	m, s := agentToolsFixture(80, 24)
	m = pressKey(m, "right")
	m = pressKey(m, "/")
	m = typeText(m, "react")
	m = pressKey(m, "enter")
	if len(m.agentSkills()) != 1 {
		t.Fatal("name filter did not select one skill")
	}
	m = pressKey(m, " ")
	if len(s.toggles) != 1 || s.toggles[0].Project != "homelab" || !s.toggles[0].On || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatal("must use project toggle for preview")
	}
	m = pressKey(m, "enter")
	if len(s.applied) != 1 || len(m.tl.agentPending["homelab"]) != 0 {
		t.Fatal("confirmation did not consume pending change")
	}
}

func TestToolsAgentsBatchAppliesMatchingTargets(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	m = pressKey(m, "esc")
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if len(s.applied) != 0 || len(s.imports) != 0 || len(s.toggles) != 3 {
		t.Fatal("batch must preview each skill without importing")
	}
	m = pressKey(m, "enter")
	if len(s.applied) != 2 || s.applied[0] != "toggle_2" || s.applied[1] != "toggle_3" || len(s.plans) != 0 {
		t.Fatalf("toggles %v applied %v", s.toggles, s.applied)
	}
	if s.toggles[1].On || !s.toggles[2].On || s.toggles[1].Project != "homelab" || s.toggles[2].Project != "homelab" {
		t.Fatal(s.toggles)
	}
}

func TestToolsAgentsProjectStateAndCosts(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	for i, p := range m.toolsProjects() {
		if p == "tenebrae" {
			m.tl.agentRow = i
		}
	}
	text := plain(m)
	for _, want := range []string{"[x] react-best-practices", "this project only", "2 skills on", "~360"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
	m.tl.search = "no-match-in-all-tools"
	if len(m.agentSkills()) != 4 {
		t.Fatal("all-tools filter leaked into agent skills")
	}
}

func TestToolsAgentsPlanningProgressAndTimeouts(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m.tl.agentPending = map[string]map[string]bool{"homelab": {"archify": false, "found-skill": true, "react-best-practices": true}}
	var deadlines []time.Time
	s.toggleErr = errors.New("fixture refusal")
	s.toggleHook = func(ctx context.Context, _ protocol.ToolToggleRequest) {
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) < 119*time.Second {
			t.Errorf("deadline %v", d)
		}
		deadlines = append(deadlines, d)
	}
	model, cmd := m.reviewAgentChanges("homelab")
	m = model.(Model)
	if !strings.Contains(strings.ToLower(strings.Join(m.tl.modal.lines, " ")), "planning 1 of 3: archify") {
		t.Errorf("missing first progress: %v", m.tl.modal.lines)
	}
	for i := 0; i < 3; i++ {
		msg := cmd()
		var next tea.Cmd
		m, next = m.onToolsModal(msg.(toolsModalMsg))
		cmd = next
		if i < 2 && !strings.Contains(strings.ToLower(strings.Join(m.tl.modal.lines, " ")), fmt.Sprintf("planning %d of 3:", i+2)) {
			t.Errorf("missing progress: %v", m.tl.modal.lines)
		}
	}
	if len(deadlines) != 3 {
		t.Fatalf("calls %d", len(deadlines))
	}
	for _, name := range []string{"archify", "found-skill", "react-best-practices"} {
		if !strings.Contains(strings.Join(m.tl.modal.lines, "\n"), name+": nothing changed:") {
			t.Errorf("missing result for %s: %v", name, m.tl.modal.lines)
		}
	}
}

func TestToolsAgentsPlanningEscapeCancels(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m.tl.agentPending = map[string]map[string]bool{"homelab": {"archify": false, "found-skill": true}}
	started := make(chan struct{})
	canceled := make(chan struct{})
	s.toggleHook = func(ctx context.Context, _ protocol.ToolToggleRequest) { close(started); <-ctx.Done(); close(canceled) }
	model, cmd := m.reviewAgentChanges("homelab")
	m = model.(Model)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(Model)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("escape did not cancel request")
	}
	msg := <-result
	_, follow := m.onToolsModal(msg.(toolsModalMsg))
	if follow != nil {
		t.Fatal("closed modal started another plan")
	}
}

func TestToolsSkillOffRowDoesNotClaimEveryProject(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	for i := range m.tl.items {
		if m.tl.items[i].Name == "archify" {
			m.tl.items[i].Desired.Assignment.Machines = []string{toolNodeM}
		}
	}
	m.rebuildTools()
	for _, line := range strings.Split(plain(m), "\n") {
		if strings.Contains(line, "[ ] archify") {
			if strings.Contains(line, "every project") {
				t.Fatalf("off row claims every project: %s", line)
			}
			return
		}
	}
	t.Fatal("off skill row missing")
}

func TestToolsSkillStateAcrossVersionsAndCheckouts(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m.snap.Projects = append(m.snap.Projects, protocol.ProjectView{Name: "homelab", NodeID: toolNodeM, Path: "/repos/homelab"})
	s.items = []protocol.ToolItem{
		toolManaged(toolSkill("versions", "a", toolLoc(toolNodeR, "research", "/skills/versions", "user", "", "claude")), "uninstalled"),
		toolManaged(toolSkill("versions", "b", toolLoc(toolNodeM, "laptop", "/skills/versions", "user", "", "codex")), "projects", "homelab"),
	}
	s.items[0].Desired.Assignment.Machines = []string{toolNodeR}
	s.items[1].Desired.Assignment.Machines = []string{toolNodeM}
	m = pressKey(m, "5")
	if on, _ := m.agentSkillState("versions", "homelab"); !on {
		t.Fatal("other checkout's version ignored")
	}
	s.items[1].Desired.Assignment.Scope = "uninstalled"
	s.items[1].Desired.Assignment.Projects = nil
	m = pressKey(m, "5")
	if on, _ := m.agentSkillState("versions", "homelab"); on {
		t.Fatal("old observed user copy overrode desired off")
	}
	m.tl.skillsFocus = true
	info := strings.Join(m.agentInfoView(100), "\n")
	if !strings.Contains(info, "2 versions: laptop and research differ") {
		t.Fatal(info)
	}
}
