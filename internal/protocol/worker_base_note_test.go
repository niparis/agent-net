package protocol

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

// A worker based on another worker's branch is told that its own worktree
// already holds that branch's head, so it must not edit the other worktree.
func TestWorkerBriefNamesAnotherWorkersBase(t *testing.T) {
	body := WorkerTaskBodyOSBase("claude", "do it", "/state/worktrees/w_2", "agent-net/w_2",
		"agent-net/w_1", "", "", WorkersSandboxed, "", settings.Snapshot{}, "")
	for _, want := range []string{
		"already starts at the head of agent-net/w_1",
		"another worker's branch",
		"never in another worker's worktree",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the brief lacks %q:\n%s", want, body)
		}
	}

	for _, base := range []string{"", "main", "feature/x"} {
		plain := WorkerTaskBodyOSBase("claude", "do it", "/state/worktrees/w_2", "agent-net/w_2",
			base, "", "", WorkersSandboxed, "", settings.Snapshot{}, "")
		if strings.Contains(plain, "another worker's branch") || strings.Contains(plain, "already starts at the head") {
			t.Errorf("base %q got another worker's branch note:\n%s", base, plain)
		}
	}
}
