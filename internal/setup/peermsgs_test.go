package setup

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/agentfiles"
)

// noTTY makes the controlling terminal unavailable.
func noTTY(t *testing.T) {
	t.Helper()
	old := openTTY
	openTTY = func() (io.ReadCloser, error) { return nil, errors.New("no tty") }
	t.Cleanup(func() { openTTY = old })
}

// answer makes the controlling terminal return the given input.
func answer(t *testing.T, input string) {
	t.Helper()
	old := openTTY
	openTTY = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(input)), nil }
	t.Cleanup(func() { openTTY = old })
}

func settingsFile(f *fixture) string { return filepath.Join(f.home, ".claude", "settings.json") }

func writeSettings(t *testing.T, f *fixture, content string) {
	t.Helper()
	p := settingsFile(f)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSettingsFile(t *testing.T, f *fixture) string {
	t.Helper()
	b, err := os.ReadFile(settingsFile(f))
	if err != nil {
		return ""
	}
	return string(b)
}

const otherKeys = "{\n  \"model\": \"opus\",\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls)\"\n    ]\n  },\n  \"verbose\": true\n}\n"

func TestPeerMessagesPromptYes(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "y\n")
	out := runAsking(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"Turn all on? [Y/n/c]", "claude peer messages accept (set by agent-net)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	got := readSettingsFile(t, f)
	for _, want := range []string{`"crossSessionInbound": "accept"`, `"model": "opus"`, `"Bash(ls)"`, `"verbose": true`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings lack %s:\n%s", want, got)
		}
	}
}

func TestPeerMessagesPromptNo(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	answer(t, "n\n")
	out := runAsking(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude peer messages off") {
		t.Errorf("output:\n%s", out)
	}
	if got := readSettingsFile(t, f); got != otherKeys {
		t.Errorf("settings changed:\n%s", got)
	}
	// The owner answered once: a rerun does not ask again, even on a terminal.
	answer(t, "y\n")
	out = run(t, Options{Join: f.newJoin(t)})
	if strings.Contains(out, "[y/N]") || readSettingsFile(t, f) != otherKeys {
		t.Errorf("asked again or changed:\n%s", out)
	}
}

func TestPeerMessagesQuestionEmptyIsAllOn(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	answer(t, "\n")
	out := runAsking(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude peer messages accept (set by agent-net)") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(readSettingsFile(t, f), `"crossSessionInbound": "accept"`) {
		t.Errorf("settings:\n%s", readSettingsFile(t, f))
	}
}

func TestPeerMessagesNoTTY(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	out := runAsking(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude peer messages off") {
		t.Errorf("output:\n%s", out)
	}
	if readSettingsFile(t, f) != otherKeys {
		t.Error("settings changed without a terminal")
	}
	if _, err := os.Stat(peerStatePath()); err == nil {
		t.Error("no-tty run recorded an answer")
	}
}

func TestPeerMessagesFlags(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	// Flags do not touch the terminal.
	old := openTTY
	openTTY = func() (io.ReadCloser, error) { t.Error("flag run opened the terminal"); return nil, errors.New("x") }
	defer func() { openTTY = old }()

	out := run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	if !strings.Contains(out, "claude peer messages off") {
		t.Errorf("no flag:\n%s", out)
	}
	if _, err := os.Stat(settingsFile(f)); err == nil {
		t.Error("--no-accept-peer-messages wrote settings")
	}
	// A missing settings file is created, with only the one key.
	out = run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	if !strings.Contains(out, "accept (set by agent-net)") {
		t.Errorf("accept flag:\n%s", out)
	}
	if got := readSettingsFile(t, f); got != "{\n  \"crossSessionInbound\": \"accept\"\n}\n" {
		t.Errorf("settings:\n%s", got)
	}
	// A rerun reports the value as agent-net's.
	out = run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude peer messages accept (set by agent-net)") {
		t.Errorf("rerun:\n%s", out)
	}
}

func TestPeerMessagesExistingValueLeftAlone(t *testing.T) {
	cases := []struct{ raw, shown string }{
		{`"accept"`, "accept (your setting)"},
		{`"hold"`, "hold (your setting)"},
		{`"refuse"`, "refuse (your setting)"},
		{`"bogus"`, "bogus (your setting)"},
	}
	for _, c := range cases {
		for _, flag := range []string{"", PeerMessagesAccept} {
			f := newFixture(t)
			fakeClaude(t, f.home)
			orig := "{\n  \"crossSessionInbound\": " + c.raw + ",\n  \"verbose\": true\n}\n"
			writeSettings(t, f, orig)
			old := openTTY
			openTTY = func() (io.ReadCloser, error) { t.Error("asked although a value exists"); return nil, errors.New("x") }
			out := run(t, Options{Join: f.newJoin(t), PeerMessages: flag})
			openTTY = old
			if !strings.Contains(out, "claude peer messages "+c.shown) {
				t.Errorf("%s flag=%q:\n%s", c.raw, flag, out)
			}
			if got := readSettingsFile(t, f); got != orig {
				t.Errorf("%s flag=%q: settings changed:\n%s", c.raw, flag, got)
			}
		}
	}
}

func TestPeerMessagesInvalidJSONUntouched(t *testing.T) {
	for _, bad := range []string{"{ not json", "[1, 2]", "{\"a\": 1} trailing", "{\n  // comment\n  \"a\": 1\n}\n"} {
		f := newFixture(t)
		fakeClaude(t, f.home)
		writeSettings(t, f, bad)
		out := run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
		if !strings.Contains(out, "is not valid JSON, left untouched") {
			t.Errorf("%q:\n%s", bad, out)
		}
		if got := readSettingsFile(t, f); got != bad {
			t.Errorf("%q: file changed to %q", bad, got)
		}
	}
}

func TestPeerMessagesHonoursClaudeConfigDir(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	dir := filepath.Join(f.home, "alt-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || !strings.Contains(string(b), `"crossSessionInbound": "accept"`) {
		t.Fatalf("settings in CLAUDE_CONFIG_DIR: %v %s", err, b)
	}
	if _, err := os.Stat(settingsFile(f)); err == nil {
		t.Error("wrote ~/.claude/settings.json although CLAUDE_CONFIG_DIR is set")
	}
}

func TestPeerMessagesSkippedWithoutClaude(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	out := run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	if !strings.Contains(out, "claude peer messages skipped (claude not found)") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(settingsFile(f)); err == nil {
		t.Error("wrote settings without claude")
	}
}

func uninstall(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Uninstall(Options{Out: &buf, Exe: "/x"}); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return norm(buf.String())
}

func TestUninstallRemovesOnlyWhatAgentNetSet(t *testing.T) {
	// agent-net set it and it is still "accept": removed, other keys kept.
	f := newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	out := uninstall(t)
	if !strings.Contains(out, "claude peer messages ok (removed crossSessionInbound)") {
		t.Errorf("output:\n%s", out)
	}
	if got := readSettingsFile(t, f); got != otherKeys {
		t.Errorf("settings not restored:\n%s", got)
	}
	if _, err := os.Stat(peerStatePath()); err == nil {
		t.Error("state file kept")
	}

	// The owner set it before agent-net: kept.
	f = newFixture(t)
	fakeClaude(t, f.home)
	owner := "{\n  \"crossSessionInbound\": \"accept\"\n}\n"
	writeSettings(t, f, owner)
	run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	out = uninstall(t)
	if !strings.Contains(out, "not set by agent-net") || readSettingsFile(t, f) != owner {
		t.Errorf("owner value removed:\n%s\n%s", out, readSettingsFile(t, f))
	}

	// agent-net set it, the owner changed it to "hold": kept.
	f = newFixture(t)
	fakeClaude(t, f.home)
	run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	changed := "{\n  \"crossSessionInbound\": \"hold\"\n}\n"
	writeSettings(t, f, changed)
	out = uninstall(t)
	if !strings.Contains(out, "value changed, left as is") || readSettingsFile(t, f) != changed {
		t.Errorf("changed value removed:\n%s\n%s", out, readSettingsFile(t, f))
	}

	// The owner answered no: uninstall touches nothing.
	f = newFixture(t)
	fakeClaude(t, f.home)
	writeSettings(t, f, otherKeys)
	run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo})
	uninstall(t)
	if readSettingsFile(t, f) != otherKeys {
		t.Error("uninstall changed settings after a no")
	}
}

func TestRefreshAndRerunDoNotTouchPeerSetting(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	os.MkdirAll(filepath.Join(f.home, ".pi"), 0o755)
	writeSettings(t, f, otherKeys)
	run(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesAccept})
	before := readSettingsFile(t, f)
	stateBefore, _ := os.ReadFile(peerStatePath())

	// What the daemon does on start (internal/agentfiles).
	agentfiles.SyncPi()
	agentfiles.SyncOpencode()
	agentfiles.SyncGuidance()
	// What an upgrade does: a plain setup run.
	run(t, Options{Join: f.newJoin(t)})

	if got := readSettingsFile(t, f); got != before {
		t.Errorf("settings changed:\n%s", got)
	}
	if stateAfter, _ := os.ReadFile(peerStatePath()); !bytes.Equal(stateBefore, stateAfter) {
		t.Error("state changed")
	}
}
