package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

// A Claude worker of a project that turned on the browser sandbox is told what
// proof to hand back. Every other worker keeps the brief it had.
func TestWorkerBriefAsksForBrowserProofOnlyWhenOptedIn(t *testing.T) {
	value := func(key, scope, v string) settings.Value {
		return settings.Value{Key: key, Level: "repo", Scope: scope, Value: json.RawMessage(v)}
	}
	snap := settings.Snapshot{Values: []settings.Value{
		value("workers.claude_browser_sandbox", "shop", "true"),
		value("workers.claude_browser_sandbox", "locked", "true"),
		value("workers.sandbox_level", "locked", `"no-network"`),
	}}
	plain := workerPreamble("claude", settings.Snapshot{}, "shop")

	got := workerPreamble("claude", snap, "shop")
	if !strings.Contains(got, WorkerBrowserProof) || !strings.Contains(WorkerBrowserProof, "two widths") {
		t.Errorf("the opted-in brief does not ask for screenshots at two widths and the test report:\n%s", got)
	}
	if strings.TrimSuffix(got, "\n- "+WorkerBrowserProof) != plain {
		t.Errorf("the opted-in brief changed more than its last line")
	}
	for name, brief := range map[string]string{
		"other project":      workerPreamble("claude", snap, "other"),
		"no-network level":   workerPreamble("claude", snap, "locked"),
		"another harness":    workerPreamble("codex", snap, "shop"),
		"opencode harness":   workerPreamble("opencode", snap, "shop"),
		"no snapshot at all": plain,
	} {
		if strings.Contains(brief, WorkerBrowserProof) {
			t.Errorf("%s got the browser proof line", name)
		}
	}
}
