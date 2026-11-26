package protocol

import (
	"github.com/niparis/agent-net/internal/settings"
	"strings"
	"testing"
)

func TestWorkerPermissionVisibility(t *testing.T) {
	for _, tc := range []struct{ level, want string }{{WorkersReadOnly, "you cannot write files; report findings only"}, {WorkersNoNetwork, "no network: git fetch, package installs and web calls will fail"}, {WorkersSandboxed, "network on"}} {
		if got := WorkerTaskBodyBriefBar("codex", WorkerBrief{Notes: "task"}, "", "", "", "", "", tc.level, "", settings.Snapshot{}, "", nil); !strings.Contains(got, tc.want) || !strings.Contains(got, "permissions: "+tc.level) {
			t.Errorf("brief %s: %s", tc.level, got)
		}
		if got := FormatWorkerNotice(WorkerNotice{Harness: "codex", Permissions: tc.level}, ""); !strings.Contains(got, "permissions: "+tc.level) {
			t.Errorf("notice: %s", got)
		}
	}
}
