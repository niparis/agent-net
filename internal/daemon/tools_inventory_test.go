package daemon

import (
	"context"
	"encoding/json"
	"log"
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
	t.Parallel()
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

func TestInventoryRescansAfterVersionChange(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	logs := &logBuf{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) {
		c.InventoryHome = home
		c.Version = "v0.4.34"
		c.Logger = log.New(logs, "", 0)
	}})
	eventually(t, "first inventory", func() bool {
		reports, err := g.gw.Store().Inventories()
		return err == nil && len(reports) == 1 && len(reports[0].Items) > 0
	})
	// Simulate an inventory stored before file manifests existed.
	old := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{{Kind: "skill", Name: "demo", Harness: "claude", Scope: "user", Location: dir, Hash: "old", State: protocol.InventoryOK}}}
	if err := g.gw.Store().SetInventory(n.d.cfg.NodeID, old); err != nil {
		t.Fatal(err)
	}
	n.cfg.Version = "v0.4.35"
	n.restart()
	eventually(t, "upgrade rescan logged", func() bool {
		return len(logs.lines("inventory: rescanning after upgrade from v0.4.34 to v0.4.35")) > 0
	})
	eventually(t, "manifest after upgrade", func() bool {
		reports, err := g.gw.Store().Inventories()
		if err != nil || len(reports) != 1 {
			return false
		}
		for _, item := range reports[0].Items {
			if item.Name == "demo" && item.Manifest != nil && len(item.Manifest.Files) == 1 {
				return true
			}
		}
		return false
	})
}

func TestPartialScanKeepsFreshManifests(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	good := filepath.Join(home, ".claude/skills/good")
	if err := os.MkdirAll(good, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("good"), 0600); err != nil {
		t.Fatal(err)
	}
	// A skill folder linked from outside the home makes the scan partial.
	outside := t.TempDir()
	linked := filepath.Join(outside, "linked")
	if err := os.MkdirAll(linked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "SKILL.md"), []byte("linked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linked, filepath.Join(home, ".claude/skills/linked")); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	eventually(t, "good skill observed", func() bool {
		items, err := g.gw.Store().ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
		if err != nil {
			return false
		}
		for _, item := range items {
			if item.Name == "good" {
				return true
			}
		}
		return false
	})
	// The stored report predates file manifests.
	old := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{{Kind: "skill", Name: "good", Harness: "claude", Scope: "user", Location: good, Hash: "old", State: protocol.InventoryOK}}}
	if err := g.gw.Store().SetInventory(n.d.cfg.NodeID, old); err != nil {
		t.Fatal(err)
	}
	cli.mustCall("inventory_scan", struct{}{})
	eventually(t, "fresh manifest kept beside the failed source", func() bool {
		items, err := g.gw.Store().ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
		if err != nil {
			return false
		}
		for _, item := range items {
			if item.Name != "good" {
				continue
			}
			for _, location := range item.Locations {
				if location.Manifest != nil && len(location.Manifest.Files) == 1 {
					return true
				}
			}
		}
		return false
	})
}

func TestToolsSkillDetailsRefreshesOneCopy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var loc, hash string
	eventually(t, "inventory item", func() bool {
		items, err := g.gw.Store().ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
		if err != nil {
			return false
		}
		for _, item := range items {
			if item.Name == "demo" && len(item.Locations) > 0 {
				loc, hash = item.Locations[0].Location, item.Hash
				return true
			}
		}
		return false
	})
	// Drop the details from the stored report, as an old scan would have.
	without := protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{{Kind: "skill", Name: "demo", Harness: "claude", Scope: "user", Location: loc, Hash: hash, State: protocol.InventoryOK}}}
	if err := g.gw.Store().SetInventory(n.d.cfg.NodeID, without); err != nil {
		t.Fatal(err)
	}
	raw, err := cli.call("tools_skill_details", protocol.ToolSkillScan{NodeID: n.d.cfg.NodeID, Name: "demo", Location: loc, Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	var details protocol.ToolSkillDetails
	if err = json.Unmarshal(raw, &details); err != nil || details.Manifest == nil || len(details.Manifest.Files) != 2 {
		t.Fatalf("details: %s %v", raw, err)
	}
	items, err := g.gw.Store().ToolsItems(protocol.ToolsFilter{Kind: protocol.InventorySkill}, func(string) bool { return true })
	if err != nil || len(items) != 1 || len(items[0].Locations) != 1 || items[0].Locations[0].Manifest == nil || len(items[0].Locations[0].Manifest.Files) != 2 {
		t.Fatalf("stored details: %+v %v", items, err)
	}
}

func TestToolsItemsOwnerGate(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestGatewayBackupOwnerGate(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)
	target := filepath.Join(t.TempDir(), "backup")
	if _, err := cli.call("gateway_backup", map[string]any{"path": target}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "gateway.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "skills.git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	pi, _ := helloPi(t, n, "pi-backup", "/work", true)
	if _, err := pi.call("gateway_backup", map[string]any{"path": filepath.Join(t.TempDir(), "forbidden")}); err == nil {
		t.Fatal("agent accessed gateway backup")
	}
}

func TestToolsReadFileOwnerAndPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/read-me")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var item protocol.ToolItem
	eventually(t, "inventory", func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill"})
		if err != nil {
			return false
		}
		var page protocol.ToolsPage
		if json.Unmarshal(raw, &page) != nil {
			return false
		}
		for _, v := range page.Items {
			if v.Name == "read-me" {
				item = v
				return true
			}
		}
		return false
	})
	loc := item.Locations[0]
	req := map[string]any{"item": item.ID, "location": map[string]string{"node_id": loc.NodeID, "location": loc.Location}, "path": "SKILL.md"}
	raw, err := cli.call("tools_read_file", req)
	if err != nil || !strings.Contains(string(raw), "hello") {
		t.Fatalf("read: %s %v", raw, err)
	}
	pi, _ := helloPi(t, n, "pi-read", "/work", true)
	if _, err := pi.call("tools_read_file", req); err == nil {
		t.Fatal("agent read file")
	}
	for _, path := range []string{"../SKILL.md", "/etc/passwd", "missing"} {
		req["path"] = path
		if _, err := cli.call("tools_read_file", req); err == nil {
			t.Fatalf("read %s", path)
		}
	}
	req["path"] = "SKILL.md"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("changed after scan"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.call("tools_read_file", req); err == nil {
		t.Fatal("read changed file from stale manifest")
	}

}

// Exercise the actual scan RPC and upload, not just a stored partial report.
func TestToolsPlanPartialScanEndToEnd(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"good", "bad"} {
		root := filepath.Join(home, ".claude/skills", name)
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	voice, err := os.Create(filepath.Join(home, ".claude/skills/bad/voice.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	if err = voice.Truncate(113895201); err != nil {
		t.Fatal(err)
	}
	if err = voice.Close(); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory, cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var good, bad protocol.ToolItem
	eventually(t, "partial inventory", func() bool {
		raw, e := cli.call("tools_items", map[string]any{"kind": "skill"})
		if e != nil {
			return false
		}
		var page protocol.ToolsPage
		if json.Unmarshal(raw, &page) != nil {
			return false
		}
		for _, item := range page.Items {
			switch item.Name {
			case "good":
				good = item
			case "bad":
				bad = item
			}
		}
		return good.ID != "" && bad.ID != ""
	})
	loc := good.Locations[0]
	cli.mustCall("tools_import", protocol.ToolImport{Item: good.ID, Location: protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}})
	raw, err := cli.call("tools_plan", protocol.ToolPlanRequest{Item: good.ID, Target: protocol.ToolAssignment{Scope: "uninstalled"}})
	if err != nil {
		t.Fatal(err)
	}
	var plan protocol.ToolPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) > 0 {
		t.Fatalf("unrelated partial failure blocks good skill: %s", raw)
	}
	cli.mustCall("tools_apply", protocol.ToolPlanID{ID: plan.ID})
	raw, err = cli.call("tools_plan", protocol.ToolPlanRequest{Item: bad.ID, Target: protocol.ToolAssignment{Scope: "uninstalled"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bad (", "cannot be read safely", "64 MiB", "voice.onnx", "114 MB"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("bad skill conflict lacks %q: %s", want, raw)
		}
	}
}
