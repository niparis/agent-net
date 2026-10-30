package setup

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// hooksOfFile decodes the hooks object of the settings file.
func hooksOfFile(t *testing.T, f *fixture) map[string][]struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	} `json:"hooks"`
} {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readSettingsFile(t, f)), &doc); err != nil {
		t.Fatalf("settings: %v\n%s", err, readSettingsFile(t, f))
	}
	return doc.Hooks
}

func TestHintHookIsAskedAndAddedOnYes(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "y\n")
	out := runAsking(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	if !strings.Contains(out, "Add the hook? [y/N]") || !strings.Contains(out, "claude hint hook on") {
		t.Fatalf("output:\n%s", out)
	}
	h := hooksOfFile(t, f)
	fail := h["PostToolUseFailure"]
	if len(fail) != 1 || fail[0].Matcher != "" || len(fail[0].Hooks) != 1 ||
		fail[0].Hooks[0].Type != "command" || fail[0].Hooks[0].Command != "/opt/test/agent-net hint" || fail[0].Hooks[0].Timeout != 2 {
		t.Fatalf("PostToolUseFailure: %+v", fail)
	}
	post := h["PostToolUse"]
	if len(post) != 1 || post[0].Matcher != "Bash" || post[0].Hooks[0].Command != "/opt/test/agent-net hint" {
		t.Fatalf("PostToolUse: %+v", post)
	}
	// The other settings are kept.
	got := readSettingsFile(t, f)
	for _, keep := range []string{`"model": "opus"`, `"Bash(ls)"`, `"verbose": true`} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	// A second run changes nothing and does not ask again.
	before := readSettingsFile(t, f)
	noTTY(t)
	runAsking(t, Options{PeerMessages: PeerMessagesNo})
	if readSettingsFile(t, f) != before {
		t.Error("second run changed the settings")
	}
}

func TestHintHookDefaultsToOffAndNeverAsksTwice(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "\n")
	runAsking(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	if readSettingsFile(t, f) != otherKeys {
		t.Fatalf("settings changed on a no:\n%s", readSettingsFile(t, f))
	}
	// Asked once: now even a "yes" on the terminal is not used.
	answer(t, "y\n")
	runAsking(t, Options{PeerMessages: PeerMessagesNo})
	if readSettingsFile(t, f) != otherKeys {
		t.Fatal("asked a second time")
	}
}

func TestHintHookWithoutTerminalAndFlags(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	noTTY(t)
	out := runAsking(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	if !strings.Contains(out, "agent-net setup --claude-hint-hook") || readSettingsFile(t, f) != otherKeys {
		t.Fatalf("no tty:\n%s", out)
	}
	runAsking(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookYes})
	if len(hooksOfFile(t, f)["PostToolUseFailure"]) != 1 {
		t.Fatal("flag did not add the hook")
	}
}

func TestHintHookKeepsOtherHooksAndUninstallRemovesOnlyOurs(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, `{
  "model": "opus",
  "hooks": {
    "PostToolUse": [
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "/usr/bin/fmt-check"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "notify-done"}]}
    ]
  }
}
`)
	runAsking(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookYes})
	h := hooksOfFile(t, f)
	if len(h["PostToolUse"]) != 2 || h["PostToolUse"][0].Hooks[0].Command != "/usr/bin/fmt-check" || len(h["Stop"]) != 1 {
		t.Fatalf("hooks after setup: %+v", h)
	}
	// A moved binary: setup rewrites the command and does not add a second copy.
	var out strings.Builder
	_ = out
	t.Setenv("PATH", os.Getenv("PATH"))
	r, err := newRunner(Options{Exe: "/new/place/agent-net", Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	r.PeerMessages = PeerMessagesNo
	r.claudeHintHook()
	h = hooksOfFile(t, f)
	if len(h["PostToolUse"]) != 2 || len(h["PostToolUseFailure"]) != 1 || h["PostToolUseFailure"][0].Hooks[0].Command != "/new/place/agent-net hint" {
		t.Fatalf("hooks after a move: %+v", h)
	}

	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	h = hooksOfFile(t, f)
	if _, ok := h["PostToolUseFailure"]; ok {
		t.Errorf("our PostToolUseFailure entry is still there: %+v", h)
	}
	if len(h["PostToolUse"]) != 1 || h["PostToolUse"][0].Matcher != "Edit" || len(h["Stop"]) != 1 {
		t.Fatalf("the owner's hooks changed: %+v", h)
	}
	if strings.Contains(readSettingsFile(t, f), "agent-net hint") {
		t.Error("hint command left in settings")
	}
	if _, err := os.Stat(hintStatePath()); err == nil {
		t.Error("state file left")
	}
}

func TestUninstallRemovesAnEmptyHooksObjectAndLeavesForeignSettingsAlone(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	runAsking(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookYes})
	var out strings.Builder
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatal(err)
	}
	got := readSettingsFile(t, f)
	if strings.Contains(got, "hooks") || !strings.Contains(got, `"model": "opus"`) {
		t.Fatalf("settings after uninstall:\n%s", got)
	}

	// Never added by agent-net: uninstall does not touch a similar hook.
	writeSettings(t, f, `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"/x/agent-net hint"}]}]}}`)
	os.Remove(hintStatePath())
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readSettingsFile(t, f), "agent-net hint") {
		t.Error("removed a hook that agent-net did not add")
	}
}

func TestIsHintCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"/opt/x/agent-net hint":              true,
		`"/Users/me/My Apps/agent-net" hint`: true,
		"agent-net hint":                     true,
		"/usr/bin/other hint":                false,
		"/opt/x/agent-net hint --x":          false,
		"/opt/x/agent-net status":            false,
	} {
		if isHintCommand(cmd) != want {
			t.Errorf("isHintCommand(%q) = %v", cmd, !want)
		}
	}
}
