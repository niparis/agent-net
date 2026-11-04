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
