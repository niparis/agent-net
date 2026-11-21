package setup

import (
	"os"
	"strings"
	"testing"
)

func urgentGroups(t *testing.T, f *fixture) []hookGroupOf {
	t.Helper()
	return withCommand(allHooksOfFile(t, f)["PostToolUse"], " urgent-hook")
}

// Setup adds the hook without a question, keeps the owner's settings and other
// hooks, and a second run changes nothing. Uninstall removes only the hook.
func TestUrgentHookIsAddedKeptAndRemoved(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, `{"model":"opus","hooks":{"PostToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/usr/bin/audit"}]}]}}`)
	var out strings.Builder
	r := &runner{Options: Options{Out: &out, Exe: "/opt/test/agent-net"}}
	r.claudeUrgentHook()
	// Every tool call, success or failure, so the message is seen at the next one.
	for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
		groups := withCommand(allHooksOfFile(t, f)[event], " urgent-hook")
		if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 ||
			groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Command != "/opt/test/agent-net urgent-hook" || groups[0].Hooks[0].Timeout != 3 {
			t.Fatalf("%s urgent-hook groups: %+v", event, groups)
		}
	}
	got := readSettingsFile(t, f)
	for _, keep := range []string{`"model":"opus"`, `/usr/bin/audit`} {
		if !strings.Contains(strings.ReplaceAll(got, " ", ""), keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	if !strings.Contains(out.String(), "on (hook in") {
		t.Fatalf("step output %q", out.String())
	}

	before := readSettingsFile(t, f)
	r.claudeUrgentHook()
	if readSettingsFile(t, f) != before {
		t.Error("a second run changed the settings")
	}

	r.removeUrgentHook()
	if n := strings.Count(readSettingsFile(t, f), "urgent-hook"); n != 0 {
		t.Fatalf("the hook is still there:\n%s", readSettingsFile(t, f))
	}
	if got := readSettingsFile(t, f); !strings.Contains(got, "/usr/bin/audit") || !strings.Contains(got, "opus") {
		t.Fatalf("uninstall removed more than the hook:\n%s", got)
	}
	if _, err := os.Stat(urgentStatePath()); !os.IsNotExist(err) {
		t.Fatalf("state file left: %v", err)
	}
}

// A hook added by an older binary path is replaced, not doubled, and a
// settings file that is not JSON is left alone.
func TestUrgentHookFollowsTheBinaryAndLeavesInvalidJSON(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	old := refreshExecutable
	t.Cleanup(func() { refreshExecutable = old })
	for _, exe := range []string{"/opt/one/agent-net", "/opt/two/agent-net"} {
		refreshExecutable = func() (string, error) { return exe, nil }
		refreshUrgentHook(nil)
	}
	groups := urgentGroups(t, f)
	if len(groups) != 1 || groups[0].Hooks[0].Command != "/opt/two/agent-net urgent-hook" {
		t.Fatalf("groups after a move: %+v", groups)
	}

	// A binary that is not named agent-net is left alone.
	refreshExecutable = func() (string, error) { return "/tmp/setup.test", nil }
	refreshUrgentHook(nil)
	if groups := urgentGroups(t, f); len(groups) != 1 || groups[0].Hooks[0].Command != "/opt/two/agent-net urgent-hook" {
		t.Fatalf("a foreign binary changed the hook: %+v", groups)
	}

	writeSettings(t, f, "not json")
	var out strings.Builder
	(&runner{Options: Options{Out: &out, Exe: "/opt/two/agent-net"}}).claudeUrgentHook()
	if got := readSettingsFile(t, f); got != "not json" || !strings.Contains(out.String(), "not valid JSON") {
		t.Fatalf("invalid settings: %q, step %q", got, out.String())
	}
}
