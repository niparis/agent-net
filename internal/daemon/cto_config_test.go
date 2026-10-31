package daemon

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCTOOwnerConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("OPENCODE_CONFIG", "")
	owner := filepath.Join(root, "opencode")
	if err := os.MkdirAll(filepath.Join(owner, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "opencode.jsonc"), []byte(`{ // owner config
 "provider": {"openai": {"options": {"x": true,},},}, "plugin": ["auth-plugin", "file:///tmp/agent-net.ts",], "model": "openai/test", "mcp": {"bad": {}}, "agent": {"bad": {}}, "permission": "allow", }`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.ts", "agent-net.ts"} {
		if err := os.WriteFile(filepath.Join(owner, "plugins", name), []byte("plugin"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "config")
	cfg, err := ctoOwnerConfig(dst)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["provider"] == nil || cfg["model"] != "openai/test" {
		t.Fatalf("missing owner settings: %#v", cfg)
	}
	for _, k := range []string{"mcp", "agent", "permission", "plugin", "plugins"} {
		if _, ok := cfg[k]; ok {
			t.Errorf("retained %s", k)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "opencode", "plugins")); !os.IsNotExist(err) {
		t.Fatal("owner plugins copied")
	}
}

func TestCTOBrokenOwnerConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("OPENCODE_CONFIG", filepath.Join(root, "bad.json"))
	if err := os.WriteFile(filepath.Join(root, "bad.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ctoOwnerConfig(filepath.Join(root, "private")); err == nil {
		t.Fatal("expected invalid config error")
	}
}

func TestCTOConfigFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("OPENCODE_CONFIG", filepath.Join(root, "bad.json"))
	if err := os.WriteFile(filepath.Join(root, "bad.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0)}
	data, err := d.ctoConfigData(filepath.Join(root, "private"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	agents := cfg["agents"].(map[string]any)
	if agents["cto"] == nil {
		t.Fatal("missing CTO agent")
	}
	mcp := cfg["mcp"].(map[string]any)["servers"].(map[string]any)
	if mcp["agent-net-cto"] == nil || len(cfg) != 2 {
		t.Fatalf("unexpected fallback: %s", data)
	}
	if strings.Count(logs.String(), "\n") != 1 || !strings.Contains(logs.String(), "using private defaults") {
		t.Fatalf("log: %q", logs.String())
	}
}

func TestCTONoBuiltinConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG", "")
	d := &Daemon{log: log.New(&bytes.Buffer{}, "", 0)}
	raw, err := d.ctoConfigData(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Agents map[string]struct {
			Disable     bool
			Permissions []struct{ Action, Resource, Effect string }
		}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Agents["general"].Disable {
		t.Error("general subagent enabled")
	}
	for _, action := range []string{"*", "bash", "edit", "write", "subagent", "read", "glob", "grep", "list", "webfetch", "todowrite"} {
		found := false
		for _, rule := range cfg.Agents["cto"].Permissions {
			if rule.Action == action && rule.Resource == "*" && rule.Effect == "deny" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing deny: %s", action)
		}
	}
}
