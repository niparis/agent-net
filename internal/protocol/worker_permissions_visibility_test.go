package protocol

import (
	"strings"
	"testing"
)

func TestWorkerPermissionVisibility(t *testing.T) {
	for _, tc := range []struct{ level, want string }{{WorkersReadOnly, "you cannot write files; report findings only"}, {WorkersNoNetwork, "no network: git fetch, package installs and web calls will fail"}, {WorkersSandboxed, "network on"}} {
		if got := WorkerTaskBodyWithPermissions("codex", "task", "", "", "", tc.level); !strings.Contains(got, tc.want) || !strings.Contains(got, "permissions: "+tc.level) {
			t.Errorf("brief %s: %s", tc.level, got)
		}
		if got := FormatWorkerNotice(WorkerNotice{Harness: "codex", Permissions: tc.level}, ""); !strings.Contains(got, "permissions: "+tc.level) {
			t.Errorf("notice: %s", got)
		}
	}
}
