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
		{Key: "workers.continue_prompt_text", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Finish this task."`)},
		{Key: "workers.continue_intro_branch", Level: "repo", Scope: "shop", Value: json.RawMessage(`"Inspect the branch carefully."`)},
	}}
	body := WorkerTaskBodyWithSettingsForRepo("codex", "task", "", "", "", "", "sandboxed", snap, "shop")
	if !strings.HasPrefix(body, "[agent-net worker brief] Custom worker introduction.") || !strings.Contains(body, "\n- Do the whole task.") {
		t.Fatalf("brief: %s", body)
	}
	if got := WorkerAutoContinueWithSettings(snap, "shop"); got != "[agent-net] continue: Finish this task." {
		t.Fatalf("auto continue: %s", got)
	}
	if got := ContinueIntroWithSettings("idle", "branch", snap, "shop"); !strings.Contains(got, "Inspect the branch carefully.") {
		t.Fatalf("continue: %s", got)
	}
}
