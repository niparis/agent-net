package gateway

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

// workers.claude_browser_sandbox loosens a worker's sandbox, so it is off until
// the owner turns it on for a repo. A CTO acting alone, and any agent, are
// refused; the owner and the CTO carrying the owner's words (a proposal the
// owner approved) are not.
func TestClaudeBrowserSandboxSettingIsOffAndOwnerOnly(t *testing.T) {
	s, _ := newTestStore(t)
	on := []SettingsChange{{Key: "workers.claude_browser_sandbox", Level: "repo", Scope: "shop", Value: json.RawMessage("true")}}

	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bool("workers.claude_browser_sandbox", settings.Where{Repo: "shop"}) {
		t.Fatal("the browser sandbox is on by default")
	}
	for _, actor := range []string{"CTO (minor)", "CTO (owner window)", "agent", "worker"} {
		if _, err := s.WriteSettings(on, actor, "test"); err == nil {
			t.Errorf("%s turned the browser sandbox on", actor)
		}
	}
	if snap, _ = s.SettingsSnapshot(); snap.Bool("workers.claude_browser_sandbox", settings.Where{Repo: "shop"}) {
		t.Fatal("a refused write changed the setting")
	}
	if _, err := s.WriteSettings(on, "owner (CTO proposal p_17)", "owner approved"); err != nil {
		t.Fatalf("an approved proposal: %v", err)
	}
	snap, _ = s.SettingsSnapshot()
	if !snap.Bool("workers.claude_browser_sandbox", settings.Where{Repo: "shop"}) {
		t.Error("the owner's write did not turn it on")
	}
	if snap.Bool("workers.claude_browser_sandbox", settings.Where{Repo: "other"}) {
		t.Error("the setting is on for a repo the owner did not name")
	}
	// A network value would turn it on everywhere, so the key is repo-only.
	network := []SettingsChange{{Key: "workers.claude_browser_sandbox", Level: "network", Scope: "network", Value: json.RawMessage("true")}}
	if _, err := s.WriteSettings(network, "owner", "test"); err == nil {
		t.Error("the owner could turn the browser sandbox on for every repo")
	}
}
