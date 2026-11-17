package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup says once what the OS sandbox of workers does here: ok, or the reason
// and the fix when the tool is missing, or nothing to do when node.json turns
// it off for every project.
func TestSetupSaysWhatTheWorkerSandboxDoes(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)

	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "worker sandbox ok (bubblewrap: Pi and OpenCode workers run in it by default)") {
		t.Errorf("ready:\n%s", out)
	}

	osSandboxCheck = func() (string, error) {
		return "", errors.New("bubblewrap (bwrap) is not installed: install it (Debian and Ubuntu: `sudo apt install bubblewrap`)")
	}
	out = run(t, Options{})
	if n := strings.Count(out, "worker sandbox"); n != 1 {
		t.Errorf("the sandbox is named %d times:\n%s", n, out)
	}
	for _, want := range []string{"not ready: Pi and OpenCode workers are refused", "sudo apt install bubblewrap"} {
		if !strings.Contains(out, want) {
			t.Errorf("not ready lacks %q:\n%s", want, out)
		}
	}

	path := filepath.Join(f.home, "cfg", "node.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	node["workers"] = map[string]any{"os_sandbox_off": "*"}
	raw, _ = json.Marshal(node)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out = run(t, Options{})
	if !strings.Contains(out, "worker sandbox off for every project (workers.os_sandbox_off in node.json)") || strings.Contains(out, "not ready") {
		t.Errorf("turned off:\n%s", out)
	}
}

// On macOS the sandbox is opt-in for now, and setup says so instead of
// checking for a tool the default does not use.
func TestSetupSaysTheMacSandboxIsOptIn(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	old := osSandboxDefaultOn
	osSandboxDefaultOn = false
	t.Cleanup(func() { osSandboxDefaultOn = old })
	osSandboxCheck = func() (string, error) { t.Error("the check ran where the default is off"); return "", nil }

	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "worker sandbox no OS sandbox on macOS yet: opt in with workers.os_sandbox in node.json") {
		t.Errorf("macOS line:\n%s", out)
	}
}
