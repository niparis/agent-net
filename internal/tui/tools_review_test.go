package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolsReviewDescribesSelectedAgent(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	m = pressKey(m, "esc")
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	for _, want := range []string{"Switch off archify in homelab.", "archify moves from every project to 12 projects", "Switch on found-skill in homelab."} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %q: %s", want, plain(m))
		}
	}
	if len(s.applied) != 0 || len(s.imports) != 0 {
		t.Fatal("review changed files")
	}
	m = pressKey(m, "d")
	if !strings.Contains(plain(m), "/home/n/.agents/skills/x") {
		t.Fatal(plain(m))
	}
}

// A user-level skill switched off in one project keeps serving the other
// projects. The plan says so before anything happens, and apply reports the
// conversion targets instead of "Nothing changed".
func TestToolsReviewUserLevelConversion(t *testing.T) {
	m, s := agentToolsFixture(80, 24)
	var projects []string
	for _, p := range m.toolsProjects() {
		if p != "homelab" {
			projects = append(projects, p)
		}
	}
	plan := protocol.ToolPlan{ID: "toggle_1", Item: "skill:archify:a", Operation: "skill_member",
		ToggleProject: "homelab", Assignment: protocol.ToolAssignment{Scope: "projects", Projects: projects, Machines: []string{toolNodeR}}}
	var applied []protocol.ToolTargetOutcome
	for _, p := range projects {
		plan.Targets = append(plan.Targets, protocol.ToolPlanTarget{NodeID: toolNodeR, Machine: "research", Project: p, Harness: "claude",
			Action: "copy", State: "pending", Destination: "/src/" + p + "/.claude/skills/archify"})
		applied = append(applied, protocol.ToolTargetOutcome{NodeID: toolNodeR, Machine: "research", Project: p, Action: "copy", State: "applied"})
	}
	plan.Targets = append(plan.Targets, protocol.ToolPlanTarget{NodeID: toolNodeR, Machine: "research", Action: "remove",
		State: "pending", Source: "/home/n/.claude/skills/archify"})
	applied = append(applied, protocol.ToolTargetOutcome{NodeID: toolNodeR, Machine: "research", Action: "remove", State: "applied"})
	s.togglePlan = &plan
	s.applyResult = &protocol.ToolApplyResult{Targets: applied}

	m = pressKey(m, "right")
	m = pressKey(m, " ")
	text := plain(m)
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"Switch off archify in homelab.",
		"is installed for every project on research",
		"copies it into the",
		"other 12 projects and removes the user-level copy.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Nothing changed") {
		t.Fatalf("appliable plan claims nothing changed:\n%s", text)
	}
	golden(t, "tools_agents_userlevel_plan_80x24", m.testView())

	m = pressKey(m, "enter")
	text = plain(m)
	if !strings.Contains(text, "Applied: 13 targets") || strings.Contains(text, "Nothing changed") {
		t.Fatalf("result:\n%s", text)
	}
	golden(t, "tools_agents_userlevel_result_80x24", m.testView())
}

func TestToolsReviewUserLevelAlreadyOn(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	s.togglePlan = &protocol.ToolPlan{ID: "toggle_1", Item: "skill:archify:a", Assignment: protocol.ToolAssignment{Scope: "global"}}
	m.tl.agentPending = map[string]map[string]bool{"homelab": {"archify": true}}
	model, cmd := m.reviewAgentChanges("homelab")
	m = model.(Model)
	m, _ = m.onToolsModal(cmd().(toolsModalMsg))
	text := plain(m)
	flat := strings.Join(strings.Fields(text), " ")
	if !strings.Contains(flat, "archify is already on for every project.") {
		t.Fatalf("missing already-on truth:\n%s", text)
	}
	if strings.Contains(flat, "no eligible target") {
		t.Fatalf("already-on plan has no explanation:\n%s", text)
	}
}

func TestToolsSingularCounts(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	if !strings.Contains(plain(m), "1 skill is installed outside agent-net") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "i")
	if !strings.Contains(plain(m), "Import all · 1 skill─") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "esc")
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "Review 1 change for homelab") || !strings.Contains(plain(m), "Switch off archify in homelab.") {
		t.Fatal(plain(m))
	}
}

func TestToolsCompactActionKeys(t *testing.T) {
	m, _ := agentToolsFixture(120, 34)
	for _, focus := range []string{"agents", "skills"} {
		if focus == "skills" {
			m = pressKey(m, "right")
		}
		lines := m.toolsKeyLines(120)
		if len(lines) != 1 {
			t.Fatalf("%s must fit one line: %v", focus, lines)
		}
		text := ansi.Strip(lines[0])
		for _, bad := range []string{"j/down", "enter/y", "right/enter", "and apply"} {
			if strings.Contains(text, bad) {
				t.Errorf("alias-heavy key bar: %s", text)
			}
		}
		if !strings.Contains(text, "↑↓ move") {
			t.Fatal(text)
		}
		if focus == "agents" && !strings.Contains(text, "→ skills") {
			t.Fatal(text)
		}
		if focus == "skills" && (!strings.Contains(text, "← projects") || !strings.Contains(text, "enter on/off")) {
			t.Fatal(text)
		}
	}
}

func TestToolsSingleReviewKeepsExactFolders(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, " ")
	if !strings.Contains(plain(m), "Switch off archify in homelab.") || len(s.toggles) != 1 || len(s.applied) != 0 {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "d")
	if !m.tl.modal.showPaths || len(m.tl.modal.paths) == 0 || !strings.Contains(plain(m), "/home/n/") {
		t.Fatal("exact destination folders missing: " + plain(m))
	}
}

func TestToolsOneItemAndTokenCounts(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	s.items = s.items[:1]
	s.items[0].StartTokens = 1
	m = pressKey(m, "5")
	if !strings.Contains(plain(m), "1 skill on · ~1 token per session") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "tab")
	if !strings.Contains(plain(m), "1 tool") || strings.Contains(plain(m), "1 tools") {
		t.Fatal(plain(m))
	}
}

func TestToolsModalKeepsSlashFilterKey(t *testing.T) {
	md := &toolsModal{foot: []string{"/", "filter", "j/k", "move", "enter/y", "apply"}}
	parts := modalKeys(md)
	if parts[0] != "/" || parts[2] != "↑↓" || parts[4] != "enter" {
		t.Fatalf("display keys: %v", parts)
	}
}
