package daemon

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Without the opt-in a worker's Bash sandbox is what it was before the browser
// profile existed: this exact JSON, with no network section.
func TestClaudeWorkerSandboxWithoutBrowserOptInIsUnchanged(t *testing.T) {
	dir := "/state/worktrees/w_1"
	got, err := json.Marshal(claudeWorkerSandbox(protocol.WorkersSandboxed, dir, []string{dir, "/tmp/go-cache"}, false))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"allowUnsandboxedCommands":false,"enabled":true,"filesystem":{"allowWrite":["/state/worktrees/w_1","/tmp/go-cache"]}}`
	if string(got) != want {
		t.Errorf("sandbox changed without the opt-in:\n got %s\nwant %s", got, want)
	}
}

// The profile adds the network keys a browser check needs and nothing else:
// writes stay in the worktree, and a command still cannot leave the sandbox.
func TestClaudeBrowserSandboxAddsOnlyNetworkKeys(t *testing.T) {
	dir := "/state/worktrees/w_1"
	writes := []string{dir, "/tmp/go-cache"}
	plain := claudeWorkerSandbox(protocol.WorkersSandboxed, dir, writes, false)
	browser := claudeWorkerSandbox(protocol.WorkersSandboxed, dir, writes, true)

	network, ok := browser["network"].(map[string]any)
	if !ok {
		t.Fatalf("no network section: %v", browser)
	}
	if network["allowLocalBinding"] != true {
		t.Errorf("a worker cannot listen on 127.0.0.1: %v", network)
	}
	if len(stringList(t, network["allowMachLookup"], "allowMachLookup")) == 0 {
		t.Errorf("Chromium cannot reach its Mach services: %v", network)
	}
	for k := range network {
		if k != "allowLocalBinding" && k != "allowMachLookup" {
			t.Errorf("unexpected network key %q: the profile must stay narrow", k)
		}
	}
	if browser["allowUnsandboxedCommands"] != false || browser["enabled"] != true {
		t.Errorf("the sandbox is weaker than before: %v", browser)
	}
	for _, k := range []string{"enabled", "allowUnsandboxedCommands", "filesystem"} {
		a, _ := json.Marshal(plain[k])
		b, _ := json.Marshal(browser[k])
		if string(a) != string(b) {
			t.Errorf("%s changed: %s, was %s", k, b, a)
		}
	}
	for _, level := range []string{protocol.WorkersNoNetwork, protocol.WorkersReadOnly} {
		if _, ok := claudeWorkerSandbox(level, dir, writes, true)["network"]; ok {
			t.Errorf("the %s level got the browser profile", level)
		}
	}
}

// The profile is per project and off by default: only a project the owner set
// gets it, and a worker started in another project keeps today's sandbox.
func TestClaudeWorkerSettingsBrowserSandboxIsPerProject(t *testing.T) {
	d := &Daemon{}
	on := json.RawMessage("true")
	snap := settings.Snapshot{Values: []settings.Value{
		{Key: "workers.claude_browser_sandbox", Level: "repo", Scope: "shop", Value: on},
		{Key: "workers.claude_browser_sandbox", Level: "repo", Scope: "locked", Value: on},
		{Key: "workers.sandbox_level", Level: "repo", Scope: "locked", Value: json.RawMessage(`"no-network"`)},
	}}
	d.settingsSnapshot.Store(&snap)

	sandboxOf := func(project, level string) map[string]any {
		w := &worker{id: "w_1", project: project, path: t.TempDir(), permissions: level}
		return d.claudeWorkerSettings(w, nil)["sandbox"].(map[string]any)
	}
	if network, ok := sandboxOf("shop", protocol.WorkersSandboxed)["network"].(map[string]any); !ok || network["allowLocalBinding"] != true {
		t.Errorf("the opted-in project has no browser sandbox: %v", network)
	}
	if sandbox := sandboxOf("other", protocol.WorkersSandboxed); sandbox["network"] != nil {
		t.Errorf("a project that did not opt in got a network section: %v", sandbox)
	}
	if sandbox := sandboxOf("locked", protocol.WorkersNoNetwork); sandbox["network"] != nil {
		t.Errorf("a no-network worker got the browser profile: %v", sandbox)
	}
}
