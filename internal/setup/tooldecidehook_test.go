package setup

import (
	"os"
	"strings"
	"testing"
)

func toolDecideGroups(t *testing.T, f *fixture) []hookGroupOf {
	t.Helper()
	return withCommand(hooksOfFile(t, f)["PreToolUse"], " tool-decide")
}

// Setup adds the hook without a question, keeps the owner's settings and other
// hooks, and a second run changes nothing. Uninstall removes only the hook.
func TestToolDecideHookIsAddedKeptAndRemoved(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/usr/bin/audit"}]}]}}`)
	var out strings.Builder
	r := &runner{Options: Options{Out: &out, Exe: "/opt/test/agent-net"}}
	r.claudeToolDecideHook()
	groups := toolDecideGroups(t, f)
	if len(groups) != 1 || groups[0].Matcher != "Bash|mcp__agent-net__.*" || len(groups[0].Hooks) != 1 ||
		groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Command != "/opt/test/agent-net tool-decide" || groups[0].Hooks[0].Timeout != 2 {
		t.Fatalf("PreToolUse tool-decide groups: %+v", groups)
	}
	got := readSettingsFile(t, f)
	for _, keep := range []string{`"model":"opus"`, `/usr/bin/audit`} {
		if !strings.Contains(strings.ReplaceAll(got, " ", ""), keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	if !strings.Contains(out.String(), "claude tool-decide hook on") {
		t.Fatalf("step output %q", out.String())
	}

	before := readSettingsFile(t, f)
	r.claudeToolDecideHook()
	if readSettingsFile(t, f) != before {
		t.Error("a second run changed the settings")
	}

	r.removeToolDecideHook()
	if n := strings.Count(readSettingsFile(t, f), "tool-decide"); n != 0 {
		t.Fatalf("the hook is still there:\n%s", readSettingsFile(t, f))
	}
	if got := readSettingsFile(t, f); !strings.Contains(got, "/usr/bin/audit") || !strings.Contains(got, "opus") {
		t.Fatalf("uninstall removed more than the hook:\n%s", got)
	}
	if _, err := os.Stat(toolDecideStatePath()); !os.IsNotExist(err) {
		t.Fatalf("state file left: %v", err)
	}
}

// A hook added by an older binary path is replaced, not doubled, and a
// settings file that is not JSON is left alone.
func TestToolDecideHookFollowsTheBinaryAndLeavesInvalidJSON(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	old := refreshExecutable
	t.Cleanup(func() { refreshExecutable = old })
	for _, exe := range []string{"/opt/one/agent-net", "/opt/two/agent-net"} {
		refreshExecutable = func() (string, error) { return exe, nil }
		refreshToolDecideHook(nil)
	}
	groups := toolDecideGroups(t, f)
	if len(groups) != 1 || groups[0].Hooks[0].Command != "/opt/two/agent-net tool-decide" {
		t.Fatalf("groups after a move: %+v", groups)
	}

	// A binary that is not named agent-net is left alone.
	refreshExecutable = func() (string, error) { return "/tmp/setup.test", nil }
	refreshToolDecideHook(nil)
	if groups := toolDecideGroups(t, f); len(groups) != 1 || groups[0].Hooks[0].Command != "/opt/two/agent-net tool-decide" {
		t.Fatalf("a foreign binary changed the hook: %+v", groups)
	}

	writeSettings(t, f, "not json")
	var out strings.Builder
	(&runner{Options: Options{Out: &out, Exe: "/opt/two/agent-net"}}).claudeToolDecideHook()
	if got := readSettingsFile(t, f); got != "not json" || !strings.Contains(out.String(), "not valid JSON") {
		t.Fatalf("invalid settings: %q, step %q", got, out.String())
	}
}
