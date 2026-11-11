package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudesettings"
)

func mouseSetupRunner(t *testing.T) (*runner, *bytes.Buffer) {
	t.Helper()
	fakeClaude(t, t.TempDir())
	var out bytes.Buffer
	return &runner{Options: Options{Out: &out}}, &out
}

func TestClaudeMouseSettingsStepWritesTheKey(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r, out := mouseSetupRunner(t)
	if err := os.WriteFile(claudeSettingsPath(), []byte(`{"theme":"dark","env":{"OTHER":"kept"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.claudeMouseSettings()
	raw, err := os.ReadFile(claudeSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	env, _ := doc["env"].(map[string]any)
	if doc["theme"] != "dark" || env["OTHER"] != "kept" || env[claudesettings.MouseKey] != claudesettings.MouseOn {
		t.Fatalf("settings = %s", raw)
	}
	if !strings.Contains(out.String(), claudeMouseStepName) || !strings.Contains(out.String(), "ok") {
		t.Fatalf("step output = %q", out.String())
	}
	// A second setup run leaves the value alone and says so.
	out.Reset()
	r.claudeMouseSettings()
	if !strings.Contains(out.String(), "already on") {
		t.Fatalf("second step output = %q", out.String())
	}
}

func TestClaudeMouseSettingsStepCreatesTheFile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r, _ := mouseSetupRunner(t)
	r.claudeMouseSettings()
	value, present, err := claudesettings.MouseValue(claudeSettingsPath())
	if err != nil || !present || value != claudesettings.MouseOn {
		t.Fatalf("value=%q present=%v err=%v", value, present, err)
	}
}

func TestClaudeMouseSettingsStepLeavesInvalidJSON(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r, out := mouseSetupRunner(t)
	bad := []byte("nope")
	if err := os.WriteFile(claudeSettingsPath(), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	r.claudeMouseSettings()
	got, err := os.ReadFile(claudeSettingsPath())
	if err != nil || string(got) != string(bad) {
		t.Fatalf("file changed: %q %v", got, err)
	}
	if !strings.Contains(out.String(), "not valid JSON") {
		t.Fatalf("step output = %q", out.String())
	}
}

func TestUninstallClaudeMouseSettingRemovesOnlyTheKey(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(claudeSettingsPath())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"CLAUDE_CODE_DISABLE_MOUSE":"0","OTHER":"kept"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := mouseSetupRunner(t)
	r.removeClaudeMouseSettings()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	env, _ := doc["env"].(map[string]any)
	if doc["theme"] != "dark" || env["OTHER"] != "kept" {
		t.Fatalf("other keys changed: %s", raw)
	}
	if _, set := env[claudesettings.MouseKey]; set {
		t.Fatalf("mouse key stayed: %s", raw)
	}
}

func TestUninstallClaudeMouseSettingKeepsAValueThatIsNotOurs(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := claudeSettingsPath()
	if err := os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_DISABLE_MOUSE":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := mouseSetupRunner(t)
	r.removeClaudeMouseSettings()
	value, present, err := claudesettings.MouseValue(path)
	if err != nil || !present || value != "1" {
		t.Fatalf("value=%q present=%v err=%v", value, present, err)
	}
}
