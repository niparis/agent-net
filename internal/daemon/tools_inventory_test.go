package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
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

func TestToolsItemsOwnerGate(t *testing.T) {
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
	eventually(t, "tools items", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		return err == nil && strings.Contains(string(raw), `"name":"demo"`)
	})
	pi, _ := helloPi(t, n, "pi-tools", "/work", true)
	if _, err := pi.call("tools_items", map[string]any{}); err == nil {
		t.Fatal("agent accessed tools_items")
	}
	if _, err := pi.call("tools_item", map[string]any{"id": "x"}); err == nil {
		t.Fatal("agent accessed tools_item")
	}
}

func TestFederationAppearsInInventory(t *testing.T) {
	useHelper(t)
	home := t.TempDir()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	cli.mustCall("mcp_add", protocol.MCPAdd{Server: stdioSpec("fixture")})
	eventually(t, "federation in inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "mcp"})
		return err == nil && strings.Contains(string(raw), `"source":"agent-net federation"`)
	})
}

func TestProjectRegistrationRefreshesTools(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "demo")
	dir := filepath.Join(root, ".agents/skills/example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("example"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	cli.mustCall("inventory_scan", map[string]any{})
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{root}})
	eventually(t, "project skill", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"project": "demo"})
		return err == nil && strings.Contains(string(raw), `"name":"example"`)
	})
}

func TestToolsImportFromOwnerPreservesSource(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/import-me")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(src, []byte("fixture skill"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "tool inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill", "harness": "claude"})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "import-me" {
				item = v
				return true
			}
		}
		return false
	})
	loc := item.Locations[0]
	req := map[string]any{"item": item.ID, "location": map[string]string{"node_id": loc.NodeID, "location": loc.Location}}
	if _, err := cli.call("tools_import", req); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "fixture skill" {
		t.Fatalf("source changed: %q %v", got, err)
	}
	pi, _ := helloPi(t, n, "pi-import", "/work", true)
	if _, err := pi.call("tools_import", req); err == nil {
		t.Fatal("agent imported skill")
	}
}

func TestToolsVersionsAndExport(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/versioned")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("version contents"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill", "harness": "claude"})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == "versioned" {
				item = v
				return true
			}
		}
		return false
	})
	loc := item.Locations[0]
	req := map[string]any{"item": item.ID, "location": map[string]string{"node_id": loc.NodeID, "location": loc.Location}}
	cli.mustCall("tools_import", req)
	cli.mustCall("tools_import", req)
	raw := cli.mustCall("tools_versions", map[string]any{"item": item.ID})
	var versions struct {
		Versions []struct {
			Hash string `json:"hash"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(raw, &versions); err != nil || len(versions.Versions) != 1 {
		t.Fatalf("versions: %s %v", raw, err)
	}
	raw = cli.mustCall("tools_export", map[string]any{"item": item.ID, "version": versions.Versions[0].Hash})
	var bundle protocol.SkillBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := inventory.UnpackSkill(bundle.Data, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "SKILL.md"))
	if err != nil || string(b) != "version contents" {
		t.Fatalf("export %q %v", b, err)
	}
}
