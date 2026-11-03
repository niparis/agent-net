package tui

import (
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestTogglePlanConflictIsSpecificAndUnique(t *testing.T) {
	p := protocol.ToolPlan{Conflicts: []string{"files changed"}, Targets: []protocol.ToolPlanTarget{{Machine: "host", Project: "demo", Action: "copy", State: "conflict", Conflict: "files changed"}, {Machine: "host", Project: "demo", Action: "copy", State: "conflict", Conflict: "files changed"}}}
	lines := strings.Join(togglePlanLines(p, protocol.ToolAssignment{}, "sample", "demo", false), "\n")
	if strings.Count(lines, "files changed") != 1 || !strings.Contains(lines, "host demo: files changed") {
		t.Fatal(lines)
	}
}

func TestTogglePlanSkippedWithoutConflicts(t *testing.T) {
	p := protocol.ToolPlan{Skipped: []protocol.ToolPlanSkip{{Machine: "host", Project: "demo", Reason: "destination contains a symlink"}}, Targets: []protocol.ToolPlanTarget{{Machine: "host", Action: "remove", Source: "/home/test/.codex/skills/sample", State: "pending"}}}
	lines := strings.Join(togglePlanLines(p, protocol.ToolAssignment{}, "sample", "demo", false), "\n")
	if strings.Contains(lines, "! ") || !strings.Contains(lines, "Skipped, cannot hold project skills: demo (its skill folder is a symlink)") || !strings.Contains(lines, "to agent-net's backup") {
		t.Fatal(lines)
	}
}

func TestToggleSummaryCountsProjectsOnce(t *testing.T) {
	res := protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{Machine: "host", Project: "one", Action: "copy", State: "ok"}, {Machine: "host", Project: "one", Action: "copy", State: "ok"}, {Machine: "host", Source: "/home/test/.codex/skills/sample", Action: "remove", State: "ok"}}}
	line := toggleOutcomeSummary("demo", false, res)
	if !strings.Contains(line, "Copied to 1 project on host") || !strings.Contains(line, "moved ~/.codex/skills/sample to backup") {
		t.Fatal(line)
	}
}

func TestToggleSummaryDoesNotClaimPendingSwitchFinished(t *testing.T) {
	res := protocol.ToolApplyResult{Targets: []protocol.ToolTargetOutcome{{Machine: "host", Project: "one", Action: "copy", State: "ok"}, {Machine: "host", Action: "remove", State: "pending", Reason: "waiting for the machine to report"}}}
	line := toggleOutcomeSummary("demo", false, res)
	if strings.Contains(line, "Switched off") || !strings.Contains(line, "waiting for the machine to report") {
		t.Fatal(line)
	}
}

func TestTogglePlanExample(t *testing.T) {
	plan := protocol.ToolPlan{Assignment: protocol.ToolAssignment{Scope: "projects", Projects: make([]string, 23), Machines: []string{"node"}}, Skipped: []protocol.ToolPlanSkip{{Machine: "laptop", Project: "code-artisans", Reason: "not a Git repository"}, {Machine: "laptop", Project: "homelab-agent", Reason: "destination contains a symlink"}, {Machine: "laptop", Project: "video", Reason: "not a Git repository"}}, Targets: []protocol.ToolPlanTarget{{Machine: "laptop", Action: "remove", Source: "/Users/owner/.codex/skills/apply-project-agent-template", State: "pending"}}}
	for i := 0; i < 22; i++ {
		plan.Targets = append(plan.Targets, protocol.ToolPlanTarget{Machine: "laptop", Project: fmt.Sprintf("project-%d", i), Action: "copy", State: "pending"})
	}
	got := strings.Join(togglePlanLines(plan, protocol.ToolAssignment{Scope: "global"}, "apply-project-agent-template", "agent-net", false), "\n")
	want := "Switch off apply-project-agent-template in agent-net.\nSkipped, cannot hold project skills: code-artisans (not a Git repository), homelab-agent (its skill folder is a symlink), video (not a Git repository)\napply-project-agent-template moves from every project to 23 projects\nIt applies on one machine only; the other machines keep what they have.\nCopies to 22 projects on laptop.\nMoves ~/.codex/skills/apply-project-agent-template on laptop to agent-net's backup."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
