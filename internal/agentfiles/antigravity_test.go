package agentfiles

import (
	"encoding/json"
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
