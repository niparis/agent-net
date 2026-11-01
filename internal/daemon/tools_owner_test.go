package daemon

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolsMCPAndFolderMethodsRefuseAgents(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory})
	agent, _ := helloPi(t, n, "tools-owner-gate", "/work", true)
	for _, method := range []string{"tools_mcp_add", "tools_mcp_remove", "tools_mcp_test", "tools_mcp_list", "tools_import_folder"} {
		if _, err := agent.call(method, protocol.MCPName{Name: "fixture"}); err == nil || !strings.Contains(err.Error(), "owner") {
			t.Errorf("agent gate for %s: %v", method, err)
		}
	}
}

func TestToolsMCPListOwner(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory})
	if _, err := cliClient(t, n).call("tools_mcp_list", nil); err != nil {
		t.Fatal(err)
	}
}

func TestToolsImportFolderOnChosenMachine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "folder-skill")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Folder skill\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	owner := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory})
	host := startNode(t, g, nodeOpts{detect: fakeInventory})
	cli := cliClient(t, owner)
	if _, err := cli.call("tools_import_folder", protocol.ToolImportLocation{NodeID: host.d.cfg.NodeID, Location: dir}); err != nil {
		t.Fatal(err)
	}
	raw, err := cli.call("tools_items", map[string]any{})
	if err != nil || !strings.Contains(string(raw), "folder-skill") {
		t.Fatalf("imported skill missing: %s %v", raw, err)
	}
	var page protocol.ToolsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	var item string
	for _, v := range page.Items {
		if v.Name == "folder-skill" {
			item = v.ID
		}
	}
	text, err := cli.call("tools_read_file", protocol.SkillTextRequest{Item: item, Path: "SKILL.md"})
	if err != nil || !strings.Contains(string(text), "Folder skill") {
		t.Fatalf("read stored folder: %s %v", text, err)
	}
	agent, _ := helloPi(t, owner, "folder-agent", "/work", true)
	if _, err := agent.call("tools_import_folder", protocol.ToolImportLocation{NodeID: host.d.cfg.NodeID, Location: dir}); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("agent gate: %v", err)
	}
	for _, path := range []string{"relative", filepath.Join(dir, "missing"), filepath.Dir(dir)} {
		if _, err := cli.call("tools_import_folder", protocol.ToolImportLocation{NodeID: host.d.cfg.NodeID, Location: path}); err == nil {
			t.Fatalf("imported invalid skill folder %s", path)
		}
	}
}

func TestToolsMCPWrappersUseRegistry(t *testing.T) {
	useHelper(t)
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory})
	cli := cliClient(t, n)
	// The fixture harness answers MCP probes without any real executable.
	cli.mustCall("tools_mcp_add", protocol.MCPAdd{Server: stdioSpec("owner-fixture")})
	raw := cli.mustCall("tools_mcp_list", nil)
	if !strings.Contains(string(raw), "owner-fixture") {
		t.Fatalf("list %s", raw)
	}
	cli.mustCall("tools_mcp_test", protocol.MCPName{Name: "owner-fixture"})
	cli.mustCall("tools_mcp_remove", protocol.MCPName{Name: "owner-fixture"})
	raw = cli.mustCall("tools_mcp_list", nil)
	if strings.Contains(string(raw), "owner-fixture") {
		t.Fatalf("removed entry remains %s", raw)
	}
}

func TestToolsKeepCopyRefusesAgents(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi", detect: fakeInventory})
	agent, _ := helloPi(t, n, "copy-choice-agent", "/work", true)
	if _, err := agent.call("tools_keep_copy", protocol.ToolCopyChoice{}); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("owner gate: %v", err)
	}
}

func TestToolsKeepCopyBacksUpOtherVariant(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(home, ".claude/skills/choice")
	b := filepath.Join(home, ".agents/skills/choice")
	for path, text := range map[string]string{a: "# Chosen\n", b: "# Other\n"} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = home }})
	cli := cliClient(t, n)
	var choice protocol.ToolCopyChoice
	eventually(t, "two observed copies", func() bool {
		raw, err := cli.call("tools_items", map[string]any{})
		if err != nil {
			return false
		}
		var page protocol.ToolsPage
		if json.Unmarshal(raw, &page) != nil {
			return false
		}
		choice = protocol.ToolCopyChoice{}
		for _, item := range page.Items {
			if item.Name != "choice" {
				continue
			}
			for _, loc := range item.Locations {
				if loc.Location != a && loc.Location != b {
					continue
				}
				choice.Copies = append(choice.Copies, protocol.ToolSkillScan{NodeID: loc.NodeID, Name: item.Name, Location: loc.Location, Hash: item.Hash})
				if loc.Location == a {
					choice.Chosen = protocol.ToolImport{Item: item.ID, Location: protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}}
				}
			}
		}
		return len(choice.Copies) >= 2 && choice.Chosen.Item != ""
	})
	if _, err := cli.call("tools_keep_copy", choice); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatalf("other variant remains: %v", err)
	}
	text, err := os.ReadFile(filepath.Join(a, "SKILL.md"))
	if err != nil || string(text) != "# Chosen\n" {
		t.Fatalf("chosen changed: %s %v", text, err)
	}
	backups, err := filepath.Glob(filepath.Join(n.d.cfg.StateDir, "skill-backups", "*", "*"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("backup missing: %v %v", backups, err)
	}
	// Replaying a stale choice cannot remove the surviving chosen folder.
	if _, err := cli.call("tools_keep_copy", choice); err == nil {
		t.Fatal("stale copy choice accepted")
	}
	var remaining []protocol.ToolSkillScan
	for _, copy := range choice.Copies {
		if copy.Location == a {
			remaining = append(remaining, copy)
		}
	}
	cli.mustCall("tools_keep_copy", map[string]any{"chosen": choice.Chosen, "copies": remaining, "remove_all": true})
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatalf("remove everywhere left the chosen copy: %v", err)
	}
	raw := cli.mustCall("tools_read_file", protocol.SkillTextRequest{Item: choice.Chosen.Item, Path: "SKILL.md"})
	if !strings.Contains(string(raw), "Chosen") {
		t.Fatalf("canonical version lost: %s", raw)
	}
}
