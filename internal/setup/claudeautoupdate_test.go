package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudesettings"
)

func autoUpdateSetupRunner(t *testing.T) (*runner, *bytes.Buffer) {
	t.Helper()
	fakeClaude(t, t.TempDir())
	var out bytes.Buffer
	return &runner{Options: Options{Out: &out}}, &out
}

func TestClaudeAutoUpdateSettingsStepWritesTheKey(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r, out := autoUpdateSetupRunner(t)
	if err := os.WriteFile(claudeSettingsPath(), []byte(`{"theme":"dark","env":{"OTHER":"kept"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.claudeAutoUpdateSettings()
	raw, err := os.ReadFile(claudeSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	env, _ := doc["env"].(map[string]any)
	if doc["theme"] != "dark" || env["OTHER"] != "kept" || env[claudesettings.AutoUpdateKey] != claudesettings.AutoUpdateOff {
		t.Fatalf("settings = %s", raw)
	}
	if !strings.Contains(out.String(), claudeAutoUpdateStepName) || !strings.Contains(out.String(), "ok") {
		t.Fatalf("step output = %q", out.String())
	}
	out.Reset()
	r.claudeAutoUpdateSettings()
	if !strings.Contains(out.String(), "already off") {
		t.Fatalf("second step output = %q", out.String())
	}
}

func TestClaudeAutoUpdateSettingsStepLeavesInvalidJSON(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r, out := autoUpdateSetupRunner(t)
	bad := []byte("nope")
	if err := os.WriteFile(claudeSettingsPath(), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	r.claudeAutoUpdateSettings()
	got, err := os.ReadFile(claudeSettingsPath())
	if err != nil || string(got) != string(bad) {
		t.Fatalf("file changed: %q %v", got, err)
	}
	if !strings.Contains(out.String(), "not valid JSON") {
		t.Fatalf("step output = %q", out.String())
	}
}

func TestUninstallClaudeAutoUpdateSettingRemovesOnlyTheKey(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := claudeSettingsPath()
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"DISABLE_AUTOUPDATER":"1","OTHER":"kept"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := autoUpdateSetupRunner(t)
	r.removeClaudeAutoUpdateSettings()
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
	if _, set := env[claudesettings.AutoUpdateKey]; set {
		t.Fatalf("auto-update key stayed: %s", raw)
	}
}

func TestUninstallClaudeAutoUpdateSettingKeepsAValueThatIsNotOurs(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := claudeSettingsPath()
	if err := os.WriteFile(path, []byte(`{"env":{"DISABLE_AUTOUPDATER":"0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := autoUpdateSetupRunner(t)
	r.removeClaudeAutoUpdateSettings()
	value, present, err := claudesettings.AutoUpdateValue(path)
	if err != nil || !present || value != "0" {
		t.Fatalf("value=%q present=%v err=%v", value, present, err)
	}
}
