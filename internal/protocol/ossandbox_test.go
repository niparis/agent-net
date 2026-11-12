package protocol

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

// The permissions line names what enforces the level: the OS wrapper, the
// harness's own sandbox (no suffix), or nothing yet.
func TestWorkerPermissionsLineNamesTheWrapper(t *testing.T) {
	for _, tc := range []struct{ level, harness, os, want string }{
		{WorkersSandboxed, "pi", "bubblewrap", "permissions: sandboxed (writes only in its worktree, network on) (sandboxed by bubblewrap)"},
		{WorkersReadOnly, "opencode", "bubblewrap", "permissions: read-only (no writes) (sandboxed by bubblewrap)"},
		{WorkersSandboxed, "pi", "", "permissions: sandboxed (writes only in its worktree, network on) (enforced for Codex only for now)"},
		{WorkersSandboxed, "claude", "", "permissions: sandboxed (writes only in its worktree, network on)"},
		{WorkersReadOnly, "codex", "", "permissions: read-only (no writes)"},
		{WorkersSandboxed, "agy", "", "permissions: sandboxed (writes only in its worktree, network on)"},
		{WorkersNoNetwork, "agy", "", "permissions: no-network (writes only in its worktree, network off) (enforced for Codex only for now)"},
	} {
		if got := WorkerPermissionsLine(tc.level, tc.harness, tc.os); got != tc.want {
			t.Errorf("%s %s %q:\n got %q\nwant %q", tc.level, tc.harness, tc.os, got, tc.want)
		}
	}
	if WorkerPermissionsText(WorkersSandboxed, "pi") != WorkerPermissionsLine(WorkersSandboxed, "pi", "") {
		t.Error("the old helper differs from the line without a wrapper")
	}
	if got := WorkerEnforcer(WorkersSandboxed, "opencode", ""); got != "" {
		t.Errorf("enforcer of an unwrapped OpenCode worker: %q", got)
	}
	if got := WorkerEnforcer(WorkersSandboxed, "opencode", "bubblewrap"); got != "bubblewrap" {
		t.Errorf("enforcer of a wrapped OpenCode worker: %q", got)
	}
}

// The brief of a wrapped worker says so, and says what fails.
func TestWorkerBriefNamesTheWrapper(t *testing.T) {
	body := WorkerTaskBodyOS("pi", "do it", "/wt", "agent-net/w_1", "", "", WorkersSandboxed, "bubblewrap", settings.Snapshot{}, "")
	if !strings.Contains(body, "\n- permissions: sandboxed (writes only in its worktree, network on) (sandboxed by bubblewrap): writes outside your worktree and /tmp fail (read-only file system)") {
		t.Fatalf("brief:\n%s", body)
	}
	plain := WorkerTaskBodyWithSettings("pi", "do it", "/wt", "agent-net/w_1", "", "", WorkersSandboxed, settings.Snapshot{})
	if strings.Contains(plain, "bubblewrap") || !strings.Contains(plain, "(enforced for Codex only for now)") {
		t.Fatalf("brief without the wrapper:\n%s", plain)
	}
	ro := WorkerTaskBodyOS("opencode", "look", "", "", "", "", WorkersReadOnly, "bubblewrap", settings.Snapshot{}, "")
	if !strings.Contains(ro, "(sandboxed by bubblewrap): you cannot write files; report findings only") {
		t.Fatalf("read-only brief:\n%s", ro)
	}
}

// The completion notice has the same line.
func TestWorkerNoticeNamesTheWrapper(t *testing.T) {
	n := WorkerNotice{Status: "done", Session: "s_1", Harness: "opencode", Model: "a/b", Project: "shop", Host: "mac",
		Permissions: WorkersSandboxed, OSSandbox: "bubblewrap", RunTime: time.Minute}
	if got := FormatWorkerNotice(n, ""); !strings.Contains(got, "\npermissions: sandboxed (writes only in its worktree, network on) (sandboxed by bubblewrap)\n") {
		t.Fatalf("notice:\n%s", got)
	}
}
