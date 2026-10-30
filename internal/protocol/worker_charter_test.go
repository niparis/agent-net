package protocol

import (
	"strings"
	"testing"
)

func TestWorkerBriefCarriesTheCharterAfterTheProjectRules(t *testing.T) {
	charter := "Charter of cogforge\nmaturity: mature - careful.\naccepts: generic endpoints."
	body := WorkerTaskBodyWithCharter("opencode", "fix the tests", "/state/wt", "agent-net/w1",
		"Use the project rules.", charter, WorkersSandboxed)
	rules := strings.Index(body, "Project rules for workers\n\nUse the project rules.")
	got := strings.Index(body, charter)
	task := strings.Index(body, "\n\nTask:\nfix the tests")
	if rules < 0 || got < 0 || task < 0 {
		t.Fatalf("brief:\n%s", body)
	}
	if !(rules < got && got < task) {
		t.Fatalf("the charter is not between the project rules and the task:\n%s", body)
	}
	if !strings.Contains(body, "permissions: sandboxed") {
		t.Fatalf("permissions missing:\n%s", body)
	}
	// A node without a charter adds nothing.
	plain := WorkerTaskBodyWithCharter("opencode", "x", "", "", "rules", "", "")
	if strings.Contains(plain, "Charter of") {
		t.Fatalf("an empty charter added text:\n%s", plain)
	}
}

func TestWorkerBriefStatesTheNodesMaturityRules(t *testing.T) {
	n := Node{Name: "cogforge", Kind: "component", Charter: Charter{Maturity: "mature - careful."}}
	body := WorkerTaskBodyWithCharter("opencode", "fix the tests", "", "", "", CharterBrief(n), WorkersSandboxed)
	for _, want := range []string{
		"maturity mature: run the suite and add tests for new behavior; merge needs your owner's or a delegate's approval; ask before deploying",
		"judge requests from consumers strictly",
		"permissions: sandboxed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("brief lacks %q:\n%s", want, body)
		}
	}
}
