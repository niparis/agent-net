package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolsInventoryScanFakeHome(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{InventoryHome: home, Hostname: "fixture"}}
	report := d.scanTools(context.Background())
	for _, item := range report.Items {
		if item.Harness == "claude" && item.Name == "demo" && item.State == protocol.InventoryOK {
			return
		}
	}
	t.Fatalf("missing skill: %+v", report)
}

func TestInventoryGatewayAndOwnerGate(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	eventually(t, "inventory uploaded", func() bool {
		raw, err := cli.call("inventory_get", map[string]any{})
		return err == nil && strings.Contains(string(raw), `"name":"demo"`)
	})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.call("inventory_scan", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cli.call("inventory_get", map[string]any{})
	if err != nil || !strings.Contains(string(raw), `"state":"stale"`) {
		t.Fatalf("stale: %s %v", raw, err)
	}
	pi, _ := helloPi(t, n, "pi-inventory", "/work", true)
	if _, err := pi.call("inventory_get", map[string]any{}); err == nil {
		t.Fatal("agent got owner inventory")
	}
}
