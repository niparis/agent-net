package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudeagents"
	"github.com/niparis/agent-net/internal/daemon"
)

func TestPolicyHookIsAskedAndAddedOnYes(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "y\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, NeedsHook: NeedsHookNo})
	if !strings.Contains(out, "Add the hook? [y/N]") || !strings.Contains(out, "claude policy hook on") {
		t.Fatalf("output:\n%s", out)
	}
	got := hooksOfFile(t, f)["UserPromptSubmit"]
	if len(got) != 1 || got[0].Matcher != "" || len(got[0].Hooks) != 1 ||
		got[0].Hooks[0].Type != "command" || got[0].Hooks[0].Command != "/opt/test/agent-net policy-hook" || got[0].Hooks[0].Timeout != 3 {
		t.Fatalf("UserPromptSubmit: %+v", got)
	}
	for _, keep := range []string{`"model": "opus"`, `"Bash(ls)"`, `"verbose": true`} {
		if !strings.Contains(readSettingsFile(t, f), keep) {
			t.Errorf("lost %s", keep)
		}
	}
	if st := loadPolicyState(); !st.Installed || !st.Asked {
		t.Errorf("state: %+v", st)
	}
	// A second run changes nothing and does not ask again.
	before := readSettingsFile(t, f)
	noTTY(t)
	runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, NeedsHook: NeedsHookNo})
	if readSettingsFile(t, f) != before {
		t.Error("second run changed the settings")
	}
}

func TestPolicyHookDefaultsToOffAndNeverAsksTwice(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "\n")
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, NeedsHook: NeedsHookNo})
	if readSettingsFile(t, f) != otherKeys {
		t.Fatalf("settings changed on a no:\n%s", readSettingsFile(t, f))
	}
	answer(t, "y\n")
	runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, NeedsHook: NeedsHookNo})
	if readSettingsFile(t, f) != otherKeys {
		t.Fatal("asked a second time")
	}
}

func TestPolicyHookWithoutTerminalAndFlags(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	noTTY(t)
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, NeedsHook: NeedsHookNo})
	if !strings.Contains(out, "agent-net setup --claude-policy-hook") || readSettingsFile(t, f) != otherKeys {
		t.Fatalf("no tty:\n%s", out)
	}
	runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookYes})
	if len(hooksOfFile(t, f)["UserPromptSubmit"]) != 1 {
		t.Fatal("flag did not add the hook")
	}
}

func TestPolicyAndHintHooksLiveSideBySideAndUninstallRemovesOnlyOurs(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	claudeHome := filepath.Join(f.home, ".claude")
	writeSettings(t, f, `{
  "hooks": {
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "/usr/bin/my-prompt-hook"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "notify-done"}]}
    ]
  }
}
`)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookYes, PolicyHook: PolicyHookYes, NeedsHook: NeedsHookNo})
	h := hooksOfFile(t, f)
	if len(h["UserPromptSubmit"]) != 2 || h["UserPromptSubmit"][0].Hooks[0].Command != "/usr/bin/my-prompt-hook" ||
		len(h["PostToolUseFailure"]) != 1 {
		t.Fatalf("hooks after setup: %+v", h)
	}
	// A subagent file the daemon wrote, and one the owner wrote.
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	if _, err := claudeagents.Ensure("agent-net-coder-sonnet", "sonnet"); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(claudeHome, "agents", "agent-net-coder-mine.md")
	os.WriteFile(mine, []byte("---\nname: agent-net-coder-mine\n---\nmine\n"), 0o644)
	theirs := filepath.Join(claudeHome, "agents", "reviewer.md")
	os.WriteFile(theirs, []byte("---\nname: reviewer\n---\n"), 0o644)

	// A moved binary: setup rewrites the command and adds no second copy.
	var out strings.Builder
	r, err := newRunner(Options{Exe: "/new/place/agent-net", Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	r.PolicyHook = PolicyHookYes
	r.claudePolicyHook()
	h = hooksOfFile(t, f)
	if len(h["UserPromptSubmit"]) != 2 || h["UserPromptSubmit"][1].Hooks[0].Command != "/new/place/agent-net policy-hook" {
		t.Fatalf("hooks after a move: %+v", h)
	}

	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	h = hooksOfFile(t, f)
	if len(h["UserPromptSubmit"]) != 1 || h["UserPromptSubmit"][0].Hooks[0].Command != "/usr/bin/my-prompt-hook" || len(h["Stop"]) != 1 {
		t.Fatalf("the owner's hooks changed: %+v", h)
	}
	if strings.Contains(readSettingsFile(t, f), "policy-hook") {
		t.Error("policy hook left in settings")
	}
	if _, err := os.Stat(daemon.PolicyHookStatePath()); err == nil {
		t.Error("state file left")
	}
	if _, err := os.Stat(filepath.Join(claudeHome, "agents", "agent-net-coder-sonnet.md")); err == nil {
		t.Error("the subagent file agent-net wrote is still there")
	}
	for _, p := range []string{mine, theirs} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a file that agent-net did not write was removed: %s", p)
		}
	}
}

func TestIsPolicyCommandIsNotTheHintCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/opt/x/agent-net policy-hook":              true,
		`"/Users/me/My Apps/agent-net" policy-hook`: true,
		"/usr/bin/other policy-hook":                false,
		"/opt/x/agent-net hint":                     false,
	} {
		if isPolicyCommand(cmd) != want {
			t.Errorf("isPolicyCommand(%q) = %v", cmd, !want)
		}
	}
	if isHintCommand("/opt/x/agent-net policy-hook") {
		t.Error("the hint matcher took the policy hook")
	}
}
