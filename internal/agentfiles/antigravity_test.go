package agentfiles

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravityPlugin(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	if r := SyncAntigravity("/tmp/agent-net"); r.Changed || r.Err != nil {
		t.Fatalf("absent: %+v", r)
	}
	if err := os.Mkdir(filepath.Join(h, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	if r := SyncAntigravity("/tmp/agent-net"); !r.Changed || r.Err != nil {
		t.Fatalf("sync: %+v", r)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	b, _ := os.ReadFile(filepath.Join(AntigravityPluginDir(), "plugin.json"))
	if err := json.Unmarshal(b, &manifest); err != nil || manifest.Name != "agent-net" {
		t.Fatalf("manifest: %s", b)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	b, _ = os.ReadFile(filepath.Join(AntigravityPluginDir(), "mcp_config.json"))
	if err := json.Unmarshal(b, &mcp); err != nil {
		t.Fatal(err)
	}
	server := mcp.Servers["agent-net"]
	if server.Command != "/tmp/agent-net" || len(server.Args) != 2 || server.Args[0] != "mcp" || server.Args[1] != "--antigravity" {
		t.Fatalf("server: %+v", server)
	}
	var hooks map[string]map[string][]struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	b, _ = os.ReadFile(filepath.Join(AntigravityPluginDir(), "hooks.json"))
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "PreInvocation", "Stop"} {
		h := hooks["agent-net"][event]
		if len(h) != 1 || h[0].Type != "command" || h[0].Timeout != 5 || !strings.Contains(h[0].Command, "hook antigravity "+event) {
			t.Fatalf("hook %s: %+v", event, h)
		}
	}
	for name, want := range map[string]string{"plugin.json": "agent-net", "mcp_config.json": "--antigravity", "hooks.json": "PreInvocation", "rules/AGENTS.md": "agent_message_reply"} {
		b, err := os.ReadFile(filepath.Join(AntigravityPluginDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Fatalf("%s: %s", name, b)
		}
		if filepath.Ext(name) == ".json" && !json.Valid(b) {
			t.Fatalf("invalid JSON %s", name)
		}
	}
	gotRules, err := os.ReadFile(filepath.Join(AntigravityPluginDir(), "rules/AGENTS.md"))
	wantRules, fixtureErr := os.ReadFile("testdata/antigravity.golden")
	if err != nil || fixtureErr != nil || string(gotRules) != string(wantRules) {
		t.Fatal("antigravity fixture mismatch")
	}
	if r := SyncAntigravity("/tmp/agent-net"); r.Changed || r.Err != nil {
		t.Fatalf("repeat: %+v", r)
	}
	if err := RemoveAntigravity(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(AntigravityPluginDir()); !os.IsNotExist(err) {
		t.Fatalf("not removed: %v", err)
	}
}

func TestAntigravityPreservesForeignFiles(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	dir := AntigravityPluginDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "rules", "AGENTS.md")
	if err := os.WriteFile(foreign, []byte("personal"), 0644); err != nil {
		t.Fatal(err)
	}
	if r := SyncAntigravity("/tmp/agent-net"); r.Changed || r.Err != nil {
		t.Fatalf("foreign plugin: %+v", r)
	}
	if err := RemoveAntigravity(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(foreign)
	if err != nil || string(b) != "personal" {
		t.Fatalf("foreign file: %q %v", b, err)
	}
}

func TestAntigravityUninstallChangedGuidance(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	if err := os.Mkdir(filepath.Join(h, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	if r := SyncAntigravity("/tmp/agent-net"); r.Err != nil {
		t.Fatal(r.Err)
	}
	path := filepath.Join(AntigravityPluginDir(), "rules", "AGENTS.md")
	if err := os.WriteFile(path, []byte(guidance.Begin+"\nchanged\n"+guidance.End+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAntigravity(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("managed file remains: %v", err)
	}
}

func TestAntigravityPreservesTextAcrossGuidanceRefreshAndUninstall(t *testing.T) {
	h := fakeHome(t)
	if err := os.Mkdir(filepath.Join(h, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	if r := SyncAntigravity("/tmp/agent-net"); r.Err != nil {
		t.Fatal(r.Err)
	}
	path := filepath.Join(AntigravityPluginDir(), "rules", "AGENTS.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, []byte("\n# Personal\nKeep this.\n")...)
	if err = os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	snap := settings.Snapshot{Values: []settings.Value{{Key: "guidance.intro_text", Level: "network", Scope: "network", Value: json.RawMessage(`"Changed introduction."`)}}}
	if r := syncAntigravity("/tmp/agent-net", snap); r.Err != nil {
		t.Fatal(r.Err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(got), "Changed introduction.") || !strings.Contains(string(got), "# Personal\nKeep this.") {
		t.Fatalf("refresh lost text: %s %v", got, err)
	}
	if err = RemoveAntigravity(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(got), guidance.Begin) || !strings.Contains(string(got), "# Personal\nKeep this.") {
		t.Fatalf("uninstall did not preserve personal text: %s %v", got, err)
	}
}
