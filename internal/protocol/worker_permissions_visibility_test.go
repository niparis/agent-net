package protocol

import (
	"github.com/niparis/agent-net/internal/settings"
	"strings"
	"testing"
)

func TestWorkerPermissionVisibility(t *testing.T) {
	tc := struct{ level, want string }{WorkersSandboxed, "network on"}
	got := WorkerTaskBodyBriefBar("codex", WorkerBrief{Notes: "task"}, "", "", "", "", "", tc.level, "", settings.Snapshot{}, "", nil)
	if !strings.Contains(got, tc.want) || !strings.Contains(got, "permissions: "+tc.level) {
		t.Errorf("brief %s: %s", tc.level, got)
	}
	if got := FormatWorkerNotice(WorkerNotice{Harness: "codex", Permissions: tc.level}, ""); !strings.Contains(got, "permissions: "+tc.level) {
		t.Errorf("notice: %s", got)
	}
}
