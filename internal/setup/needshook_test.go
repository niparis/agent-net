package setup

import (
	"os"
	"strings"
	"testing"
)

// The one consent question adds one command on four events: a permission
// request and a notification that mean a person is needed, the tool use that
// shows the session moved on, and the prompt that answers them (decision 0016).
func TestNeedsHookIsAddedWithTheOneQuestion(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "y\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, PolicyHook: PolicyHookNo})
	if !strings.Contains(out, "Turn all on? [Y/n/c]") || !strings.Contains(out, "claude needs hook on") {
		t.Fatalf("output:\n%s", out)
	}
	h := hooksOfFile(t, f)
	want := []struct{ event, matcher string }{
		{"PermissionRequest", ""},
		{"Notification", "idle_prompt|agent_needs_input|elicitation_dialog|elicitation_url_dialog"},
		{"PreToolUse", ""},
		{"UserPromptSubmit", ""},
	}
	for _, w := range want {
		groups := h[w.event]
		if len(groups) != 1 || groups[0].Matcher != w.matcher || len(groups[0].Hooks) != 1 ||
			groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Command != "/opt/test/agent-net needs-hook" ||
			groups[0].Hooks[0].Timeout != 2 {
			t.Fatalf("%s: %+v", w.event, groups)
		}
	}
	for _, keep := range []string{`"model": "opus"`, `"Bash(ls)"`, `"verbose": true`} {
		if !strings.Contains(readSettingsFile(t, f), keep) {
			t.Errorf("lost %s", keep)
		}
	}
	if st := loadNeedsState(); !st.Installed || !st.Asked {
		t.Errorf("state: %+v", st)
	}
	// A second run changes nothing and does not ask again.
	before := readSettingsFile(t, f)
	noTTY(t)
	runAskingAll(t, Options{PeerMessages: PeerMessagesNo})
	if readSettingsFile(t, f) != before {
		t.Error("second run changed the settings")
	}
}

func TestNeedsHookOffAndNeverAsksTwice(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "n\n")
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	if readSettingsFile(t, f) != otherKeys {
		t.Fatalf("settings changed on a no:\n%s", readSettingsFile(t, f))
	}
	answer(t, "y\n")
	runAskingAll(t, Options{PeerMessages: PeerMessagesNo})
	if readSettingsFile(t, f) != otherKeys {
		t.Fatal("asked a second time")
	}
}

func TestNeedsHookWithoutTerminalAndFlag(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	noTTY(t)
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	if !strings.Contains(out, "claude needs hook off") || readSettingsFile(t, f) != otherKeys {
		t.Fatalf("no tty:\n%s", out)
	}
	runAskingAll(t, Options{PeerMessages: PeerMessagesNo, NeedsHook: NeedsHookYes})
	if len(hooksOfFile(t, f)["PermissionRequest"]) != 1 {
		t.Fatal("flag did not add the hook")
	}
}

// Uninstall removes the needs hooks and leaves the owner's own hooks.
func TestNeedsHookUninstallRemovesOnlyOurs(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, `{
  "hooks": {
    "PermissionRequest": [
      {"hooks": [{"type": "command", "command": "/usr/bin/my-permission-hook"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "notify-done"}]}
    ]
  }
}
`)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, NeedsHook: NeedsHookYes})
	if len(hooksOfFile(t, f)["PermissionRequest"]) != 2 {
		t.Fatalf("hooks after setup: %+v", hooksOfFile(t, f))
	}
	var out strings.Builder
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	h := hooksOfFile(t, f)
	if len(h["PermissionRequest"]) != 1 || h["PermissionRequest"][0].Hooks[0].Command != "/usr/bin/my-permission-hook" || len(h["Stop"]) != 1 {
		t.Fatalf("the owner's hooks changed: %+v", h)
	}
	if strings.Contains(readSettingsFile(t, f), "needs-hook") {
		t.Error("needs hook left in settings")
	}
	if _, err := os.Stat(needsStatePath()); err == nil {
		t.Error("state file left")
	}
}
