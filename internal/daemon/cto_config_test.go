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
	b, _ := json.Marshal(cfg["plugin"])
	if string(b) != `["auth-plugin"]` {
		t.Fatalf("plugins: %s", b)
	}
	for _, k := range []string{"mcp", "agent", "permission"} {
		if _, ok := cfg[k]; ok {
			t.Fatalf("retained %s", k)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "opencode", "plugins", "auth.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "opencode", "plugins", "agent-net.ts")); !os.IsNotExist(err) {
		t.Fatal("agent-net plugin retained")
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
