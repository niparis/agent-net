package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

func TestWorkerRecipeTextOverrides(t *testing.T) {
	snap := settings.Snapshot{Values: []settings.Value{
		{Key: "workers.brief_preamble", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Custom worker introduction."`)},
		{Key: "workers.continue_prompt_text", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Finish this task with {{complete_yes}}."`)},
		{Key: "workers.continue_intro_branch", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Inspect the branch carefully."`)},
	}}
	body := WorkerTaskBodyWithSettingsForRepo("codex", "task", "", "", "", "", "sandboxed", snap, "shop")
	if !strings.HasPrefix(body, "[agent-net worker brief] Custom worker introduction.") || !strings.Contains(body, "\n- Do the whole task.") {
		t.Fatalf("brief: %s", body)
	}
	if got := WorkerAutoContinueWithSettings(snap, "shop"); got != "[agent-net] continue: Finish this task with complete: yes." {
		t.Fatalf("auto continue: %s", got)
	}
	if got := ContinueIntroWithSettings("idle", "branch", snap, "shop"); !strings.Contains(got, "Inspect the branch carefully.") {
		t.Fatalf("continue: %s", got)
	}
}

func TestWorkerBriefProtocolTokensAreFixed(t *testing.T) {
	for _, tc := range []struct {
		key, custom, want string
		slots             []string
	}{
		{"workers.brief_codex_complete", `"Finish with {{complete_yes}} or {{complete_no}}."`, "Finish with complete: yes or complete: no.", []string{"complete_yes", "complete_no"}},
		{"workers.brief_handback", `"Call {{handback}}."`, "Call agent_handback.", []string{"handback"}},
		{"workers.brief_handback_partial", `"Call {{handback}} with {{complete_false}}."`, "Call agent_handback with complete: false.", []string{"handback", "complete_false"}},
		{"workers.brief_blocked", `"Call {{handback}} when blocked."`, "Call agent_handback when blocked.", []string{"handback"}},
		{"workers.brief_claude", `"Use {{handback}}, {{mcp_handback}}, and {{search_handback}}."`, "Use agent_handback, mcp__agent-net__agent_handback, and select:mcp__agent-net__agent_handback.", []string{"handback", "mcp_handback", "search_handback"}},
		{"workers.continue_prompt_text", `"Finish with {{complete_yes}}."`, "Finish with complete: yes.", []string{"complete_yes"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			def, ok := settings.DefinitionFor(tc.key)
			if !ok {
				t.Fatal("missing definition")
			}
			for _, token := range []string{"agent_handback", "complete: yes", "complete: no", "complete: false"} {
				if strings.Contains(def.Default.Value, token) {
					t.Errorf("default contains literal protocol token %q", token)
				}
			}
			if _, err := def.Codec(json.RawMessage(`"No fixed tokens here."`)); err == nil {
				t.Error("accepted text without the required protocol slots")
			}
			if _, err := def.Codec(json.RawMessage(tc.custom)); err != nil {
				t.Fatalf("valid slots: %v", err)
			}
			snap := settings.Snapshot{Values: []settings.Value{{Key: tc.key, Level: "repo", Scope: "shop", Value: json.RawMessage(tc.custom)}}}
			var body string
			if tc.key == "workers.continue_prompt_text" {
				body = WorkerAutoContinueWithSettings(snap, "shop")
			} else if tc.key == "workers.brief_codex_complete" {
				body = WorkerTaskBodyWithSettingsForRepo("codex", "task", "", "", "", "", "sandboxed", snap, "shop")
			} else {
				body = WorkerTaskBodyWithSettingsForRepo("claude", "task", "", "", "", "", "sandboxed", snap, "shop")
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("custom text was not rendered with fixed tokens: %s", body)
			}
			for _, slot := range tc.slots {
				if strings.Contains(body, "{{"+slot+"}}") {
					t.Errorf("unrendered slot %s: %s", slot, body)
				}
			}
			if tc.key == "workers.brief_codex_complete" && (!strings.Contains(body, "complete: yes") || !strings.Contains(body, "complete: no")) {
				t.Error("missing codex completion tokens")
			}
			if tc.key == "workers.continue_prompt_text" && !strings.Contains(body, "complete: yes") {
				t.Error("missing continuation completion token")
			}
			if strings.Contains(tc.key, "handback") || tc.key == "workers.brief_blocked" || tc.key == "workers.brief_claude" {
				if !strings.Contains(body, "agent_handback") {
					t.Error("missing handback tool")
				}
			}
		})
	}
}

// The brief tells a worker to run only its own tests when another owner runs
// the project's test step. The owner of that step decides whether the line is
// there; the caller's own step gets no line.
func TestWorkerBriefTestScopeFollowsTestStepOwner(t *testing.T) {
	const line = "\n- Tests: run only the tests that cover your change, by name (Go: -run '^(TestA|TestB)$'), or run the project's test-changed script, which picks them from the diff. Do not run whole packages, race runs, repeat or load runs, or full suites, and run one test command at a time. The delivery test step and CI run the full packages."
	for _, tc := range []struct {
		name        string
		project     string
		assignments []settings.DeliveryAssignment
		want        bool
	}{
		{"merger owns the test step", "shop", []settings.DeliveryAssignment{{Project: "shop", Step: StepTest, Node: "/delivery/merger", Kind: "tests"}}, true},
		{"a node owns the test step", "shop", []settings.DeliveryAssignment{{Project: "shop", Step: StepTest, Node: "tenebrae", Kind: "tests"}}, true},
		{"caller owns the test step", "shop", []settings.DeliveryAssignment{{Project: "shop", Step: StepTest, Node: "/delivery/caller", Kind: "tests"}}, false},
		{"no assignment", "shop", nil, false},
		{"another project owns the test step", "shop", []settings.DeliveryAssignment{{Project: "other", Step: StepTest, Node: "/delivery/merger", Kind: "tests"}}, false},
	} {
		snap := settings.Snapshot{DeliveryPipelineCutover: true, DeliveryAssignments: tc.assignments}
		for _, harness := range []string{"claude", "codex", "opencode", "pi", "agy"} {
			t.Run(tc.name+"/"+harness, func(t *testing.T) {
				body := WorkerTaskBodyWithSettingsForRepo(harness, "do work", "", "", "", "", "sandboxed", snap, tc.project)
				if got := strings.Contains(body, line); got != tc.want {
					t.Fatalf("test scope line present = %t, want %t: %s", got, tc.want, body)
				}
				if tc.want && strings.Index(body, line) > strings.Index(body, "\n- If you are blocked") {
					t.Fatalf("test scope line comes after the blocked line: %s", body)
				}
			})
		}
	}
}

func TestWorkerBriefTestScopeTextCanBeSet(t *testing.T) {
	snap := settings.Snapshot{DeliveryPipelineCutover: true,
		DeliveryAssignments: []settings.DeliveryAssignment{{Project: "shop", Step: StepTest, Node: "/delivery/merger", Kind: "tests"}},
		Values:              []settings.Value{{Key: "workers.brief_tests", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Run only your own tests."`)}}}
	body := WorkerTaskBodyWithSettingsForRepo("claude", "task", "", "", "", "", "sandboxed", snap, "shop")
	if !strings.Contains(body, "\n- Run only your own tests.\n") {
		t.Fatalf("repo text for the test scope line was not used: %s", body)
	}
}
