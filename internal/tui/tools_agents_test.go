package tui

import (
	"fmt"
	"strings"
	"testing"

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
	for _, want := range []string{"Agents", "skills per agent", "[x] archify", "[x] estate-runbook", "[ ] found-skill", "[ ] react-best-practices", "always on", "not managed"} {
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

func TestToolsAgentsPendingPlan(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "→ off") || len(s.applied) != 0 {
		t.Fatal("toggle must remain pending")
	}
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if len(m.tl.agentPending["homelab"]) != 1 || !strings.Contains(plain(m), "always on") {
		t.Fatal("repo skill was toggled")
	}
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	jobs, why := m.agentJobs("homelab")
	if why != "" || len(jobs) != 2 {
		t.Fatalf("jobs %+v: %s", jobs, why)
	}
	if jobs[0].name != "archify" || hasStr(jobs[0].target.Projects, "homelab") || !hasStr(jobs[0].target.Projects, "tenebrae") {
		t.Fatalf("global off: %+v", jobs[0])
	}
	if jobs[1].imp == nil || strings.Join(jobs[1].target.Projects, ",") != "homelab" {
		t.Fatalf("import on: %+v", jobs[1])
	}
	m = pressKey(m, "enter")
	if m.tl.modal == nil || !strings.Contains(plain(m), "Review 2 changes") || len(s.applied) != 0 || len(s.imports) != 0 {
		t.Fatal("enter must review one batch before changes")
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
		golden(t, fmt.Sprintf("tools_agents_%dx%d", size[0], size[1]), m.View())
		m = pressKey(m, "right")
		m = pressKey(m, " ")
		m = pressKey(m, "down")
		m = pressKey(m, "down")
		m = pressKey(m, " ")
		golden(t, fmt.Sprintf("tools_agents_pending_%dx%d", size[0], size[1]), m.View())
		for _, line := range strings.Split(plain(m), "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("line too wide: %s", line)
			}
		}
		m = pressKey(m, "enter")
		golden(t, fmt.Sprintf("tools_agents_plan_%dx%d", size[0], size[1]), m.View())
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
	jobs, why := m.agentJobs("homelab")
	if why != "" || len(jobs) != 1 || strings.Join(jobs[0].target.Projects, ",") != "homelab,tenebrae" {
		t.Fatalf("must retain other agents: %+v %s", jobs, why)
	}
	m = pressKey(m, "enter")
	if len(s.plans) != 1 || len(s.applied) != 0 {
		t.Fatal("single change must plan without applying")
	}
	m = pressKey(m, "enter")
	if len(s.applied) != 1 || len(m.tl.agentPending["homelab"]) != 0 {
		t.Fatal("confirmation did not apply and consume pending change")
	}
}

func TestToolsAgentsBatchAppliesMatchingTargets(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	m = pressKey(m, "enter")
	m = pressKey(m, "enter")
	if len(s.plans) != 2 || len(s.applied) != 2 || len(s.imports) != 1 {
		t.Fatalf("plans %v applied %v imports %v", s.plans, s.applied, s.imports)
	}
	if hasStr(s.plans[0].Target.Projects, "homelab") || strings.Join(s.plans[1].Target.Projects, ",") != "homelab" {
		t.Fatalf("wrong targets: %+v", s.plans)
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
	for _, want := range []string{"[x] react-best-practices", "on for this agent", "2 skills on", "~360"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
	m.tl.search = "no-match-in-all-tools"
	if len(m.agentSkills()) != 4 {
		t.Fatal("all-tools filter leaked into agent skills")
	}
}
