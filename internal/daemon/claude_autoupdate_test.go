package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudesettings"
	"github.com/niparis/agent-net/internal/protocol"
)

// agent-net's daemon is the only updater for Claude Code, so every Claude Code
// process it starts gets DISABLE_AUTOUPDATER=1 (owner's order).

func TestClaudeLaunchEnvDisablesAutoupdater(t *testing.T) {
	env := claudeLaunchEnv([]string{"PATH=/bin", "DISABLE_AUTOUPDATER=0", "HOME=/tmp/x"})
	var values []string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == claudesettings.AutoUpdateKey {
			values = append(values, v)
		}
	}
	if len(values) != 1 || values[0] != claudesettings.AutoUpdateOff {
		t.Fatalf("launch env = %v", env)
	}
	for _, want := range []string{"PATH=/bin", "HOME=/tmp/x"} {
		found := false
		for _, kv := range env {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing from %v", want, env)
		}
	}
}

func TestClaudePlanEnvDisablesAutoupdater(t *testing.T) {
	plan := claudePlanEnv(protocol.EnterPlan{Argv: []string{"/opt/homebrew/bin/claude", "attach", "job"}})
	if plan.Env[claudesettings.AutoUpdateKey] != claudesettings.AutoUpdateOff {
		t.Fatalf("plan env = %v", plan.Env)
	}
	other := claudePlanEnv(protocol.EnterPlan{Argv: []string{"/usr/bin/opencode", "--session", "x"}})
	if _, set := other.Env[claudesettings.AutoUpdateKey]; set {
		t.Fatalf("an OpenCode plan got the Claude variable: %v", other.Env)
	}
}

func TestClaudeAutoUpdateSettingMergesAndReapplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","env":{"OTHER":"kept","DISABLE_AUTOUPDATER":"0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.claudeSettingsPath = path
	d.ensureClaudeAutoUpdateSetting(context.Background())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	env, _ := doc["env"].(map[string]any)
	if doc["theme"] != "dark" || env["OTHER"] != "kept" || env[claudesettings.AutoUpdateKey] != claudesettings.AutoUpdateOff {
		t.Fatalf("merged file: %s", raw)
	}
	// Drift is repaired on the next pass.
	if err := os.WriteFile(path, []byte(`{"env":{"DISABLE_AUTOUPDATER":"0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d.ensureClaudeAutoUpdateSetting(context.Background())
	value, present, err := claudesettings.AutoUpdateValue(path)
	if err != nil || !present || value != claudesettings.AutoUpdateOff {
		t.Fatalf("after drift value=%q present=%v err=%v", value, present, err)
	}
}

func TestClaudeAutoUpdateSettingLeavesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	bad := []byte("{ this is not json ")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	d.cfg.claudeSettingsPath = path
	d.ensureClaudeAutoUpdateSetting(context.Background())
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("file changed: %q %v", got, err)
	}
}
