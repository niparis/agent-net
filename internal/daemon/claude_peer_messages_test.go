package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When the live Claude setting lost the key that setup recorded, the daemon
// reports the difference at start and names the line to declare. It writes
// nothing.
func TestClaudePeerMessagesReportsAMissingManagedSetting(t *testing.T) {
	config := t.TempDir()
	t.Setenv("AGENT_NET_CONFIG_DIR", config)
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model": "opus"}`), 0o444); err != nil {
		t.Fatal(err)
	}
	state := `{"asked": true, "set_by_agent_net": true, "settings_path": "` + settings + `"}`
	if err := os.WriteFile(filepath.Join(config, "claude-peer-messages.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(settings)
	var logs strings.Builder
	d := &Daemon{log: log.New(&logs, "", 0), cfg: Config{claudeSettingsPath: settings}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a failed notice must not retry after the test
	d.checkClaudePeerMessages(ctx)
	for _, want := range []string{"claude peer messages", "crossSessionInbound", "Declare this line"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log %q missing %q", logs.String(), want)
		}
	}
	after, _ := os.ReadFile(settings)
	if string(before) != string(after) {
		t.Fatalf("settings changed from %q to %q", before, after)
	}
}

// A live accept value agrees with the record, so nothing is reported.
func TestClaudePeerMessagesStaysQuietWhenTheLiveValueAgrees(t *testing.T) {
	config := t.TempDir()
	t.Setenv("AGENT_NET_CONFIG_DIR", config)
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"crossSessionInbound": "accept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state := `{"asked": true, "set_by_agent_net": true}`
	if err := os.WriteFile(filepath.Join(config, "claude-peer-messages.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	d := &Daemon{log: log.New(&logs, "", 0), cfg: Config{claudeSettingsPath: settings}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.checkClaudePeerMessages(ctx)
	if logs.Len() != 0 {
		t.Fatalf("reported an agreeing setting: %s", logs.String())
	}
}
