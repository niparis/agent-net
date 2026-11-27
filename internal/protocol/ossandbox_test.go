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
		{WorkersSandboxed, "pi", "", "permissions: sandboxed (writes only in its worktree, network on) (no OS sandbox: turned off for this project in node.json)"},
		{WorkersSandboxed, "opencode", "sandbox-exec", "permissions: sandboxed (writes only in its worktree, network on) (sandboxed by sandbox-exec)"},
		{WorkersSandboxed, "pi", OSSandboxNoneMacOS, "permissions: sandboxed (writes only in its worktree, network on) (no OS sandbox on macOS yet: opt in with workers.os_sandbox in node.json)"},
		{WorkersSandboxed, "claude", "", "permissions: sandboxed (writes only in its worktree, network on)"},
		{WorkersSandboxed, "agy", "", "permissions: sandboxed (writes only in its worktree, network on)"},
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
	if got := WorkerEnforcer(WorkersSandboxed, "opencode", OSSandboxNoneMacOS); got != "" {
		t.Errorf("enforcer of a Mac worker without the wrapper: %q", got)
	}
	if got := WorkerEnforcer(WorkersSandboxed, "opencode", "bubblewrap"); got != "bubblewrap" {
		t.Errorf("enforcer of a wrapped OpenCode worker: %q", got)
	}
}

// The brief of a wrapped worker says so, and says where it may write
// (decision 0197): not /tmp, which the wrapper keeps read-only.
func TestWorkerBriefNamesTheWrapper(t *testing.T) {
	body := WorkerTaskBodyBriefBar("pi", WorkerBrief{Notes: "do it"}, "/wt", "agent-net/w_1", "", "", "", WorkersSandboxed, "bubblewrap", settings.Snapshot{}, "", nil)
	if !strings.Contains(body, "\n- permissions: sandboxed (writes only in its worktree, network on) (sandboxed by bubblewrap): you may write only in your worktree, $TMPDIR, your caches and the repository's git folder; other writes fail (read-only file system)") || strings.Contains(body, "/tmp") {
		t.Fatalf("brief:\n%s", body)
	}
	plain := WorkerTaskBodyBriefBar("pi", WorkerBrief{Notes: "do it"}, "/wt", "agent-net/w_1", "", "", "", WorkersSandboxed, "", settings.Snapshot{}, "", nil)
	if strings.Contains(plain, "bubblewrap") || strings.Contains(plain, "enforced for claude, codex and agy only") || !strings.Contains(plain, "(no OS sandbox: turned off for this project in node.json)") {
		t.Fatalf("brief without the wrapper:\n%s", plain)
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

// The policy screen says what enforces a level. Pi and OpenCode are in the OS
// sandbox by default.
func TestWorkerEnforcedLine(t *testing.T) {
	if got := WorkerEnforcedLine(WorkersSandboxed); !strings.Contains(got, "Pi and OpenCode (OS sandbox)") || strings.Contains(got, "only for now") {
		t.Errorf("sandboxed: %q", got)
	}
}

// A Claude worker on a Linux machine without socat (or bwrap) runs its shell
// commands with no OS sandbox. Its notice and its brief must say so, and
// nothing enforces the level for it.
func TestWorkerPermissionsLineSaysClaudeIsNotSandboxed(t *testing.T) {
	missing := OSSandboxNoneClaude([]string{"socat"})
	if got := WorkerPermissionsLine(WorkersSandboxed, "claude", missing); got != "permissions: NOT sandboxed: Claude sandbox needs socat, install it on the worker's machine" {
		t.Fatalf("notice: %q", got)
	}
	both := OSSandboxNoneClaude([]string{"bwrap", "socat"})
	if got := WorkerPermissionsLine(WorkersSandboxed, "claude", both); !strings.Contains(got, "Claude sandbox needs bwrap and socat, install it") {
		t.Fatalf("notice with both missing: %q", got)
	}
	if got := WorkerEnforcer(WorkersSandboxed, "claude", missing); got != "" {
		t.Errorf("enforcer of a Claude worker without its sandbox: %q", got)
	}
	if got := WorkerEnforcer(WorkersSandboxed, "claude", ""); got != "claude" {
		t.Errorf("enforcer of a Claude worker with its sandbox: %q", got)
	}
	if got := OSSandboxWrapper(missing); got != "" {
		t.Errorf("wrapper of a Claude worker without its sandbox: %q", got)
	}
	body := WorkerTaskBodyBriefBar("claude", WorkerBrief{Notes: "say ok"}, "/wt", "agent-net/w_1", "", "", "", WorkersSandboxed, missing, settings.Snapshot{}, "", nil)
	if !strings.Contains(body, "\n- permissions: NOT sandboxed: Claude sandbox needs socat, install it on the worker's machine") {
		t.Fatalf("brief:\n%s", body)
	}
}
