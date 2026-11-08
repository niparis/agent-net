package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func writeMCPJSON(t *testing.T, dir string, servers map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"mcpServers": servers})
	file := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func readLocalSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Claude Code walks up from the launch folder to find .mcp.json files. The
// nearer file wins for a name both define.
func TestClaudeMCPServersWalksUpAndNearerWins(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "tenebrae")
	child := filepath.Join(parent, "finget")
	parentFile := writeMCPJSON(t, parent, map[string]any{
		"tenebrae-library": map[string]any{"command": "just", "args": []string{"--justfile", "strategies/justfile", "library-mcp"}},
		"shared":           map[string]any{"command": "far"},
	})
	childFile := writeMCPJSON(t, child, map[string]any{"shared": map[string]any{"command": "near"}, "web": map[string]any{"type": "http", "url": "https://example.test/mcp"}})
	got := claudeMCPServers(child)
	want := []protocol.MCPServerInfo{
		{Name: "shared", Command: "near", File: childFile},
		{Name: "web", Command: "https://example.test/mcp", File: childFile},
		{Name: "tenebrae-library", Command: "just --justfile strategies/justfile library-mcp", File: parentFile},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("servers\n got %+v\nwant %+v", got, want)
	}
}

func TestClaudeMCPChooseApproveAndUndoRemovesOnlyWhatItAdded(t *testing.T) {
	dir := t.TempDir()
	writeMCPJSON(t, dir, map[string]any{"a": map[string]any{"command": "x"}, "b": map[string]any{"command": "y"}, "c": map[string]any{"command": "z"}})
	settings := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	// The owner already approved a, refused b, and has an unrelated key.
	seed := `{"permissions":{"allow":["Bash(ls)"]},"enabledMcpjsonServers":["a","other"],"disabledMcpjsonServers":["b"]}`
	if err := os.WriteFile(settings, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := claudeMCPChoose(dir, protocol.MCPApprove)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Added, []string{"b", "c"}) || !reflect.DeepEqual(res.Restored, []string{"b"}) || res.File != settings {
		t.Fatalf("result %+v", res)
	}
	got := readLocalSettings(t, dir)
	if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"a", "other", "b", "c"}) {
		t.Fatalf("enabled %v", got["enabledMcpjsonServers"])
	}
	if v, ok := got["disabledMcpjsonServers"]; ok && len(v.([]any)) != 0 {
		t.Fatalf("a refusal that would win must go: %v", v)
	}
	if got["permissions"] == nil {
		t.Fatal("unrelated keys must stay")
	}
	if _, ok := got["enableAllProjectMcpServers"]; ok {
		t.Fatal("enableAllProjectMcpServers must never be set")
	}
	// A second approval adds nothing, so its undo would change nothing.
	again, err := claudeMCPChoose(dir, protocol.MCPApprove)
	if err != nil || len(again.Added) != 0 || len(again.Restored) != 0 {
		t.Fatalf("second approval %+v %v", again, err)
	}
	if err := claudeMCPUndo(dir, protocol.MCPUndo{Choice: protocol.MCPApprove, Added: res.Added, Restored: res.Restored}); err != nil {
		t.Fatal(err)
	}
	got = readLocalSettings(t, dir)
	if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"a", "other"}) || !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"b"}) {
		t.Fatalf("undo must restore the owner's own lists: %v", got)
	}
}

func TestClaudeMCPChooseDisable(t *testing.T) {
	dir := t.TempDir()
	writeMCPJSON(t, dir, map[string]any{"lib": map[string]any{"command": "just"}})
	res, err := claudeMCPChoose(dir, protocol.MCPDisable)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Added, []string{"lib"}) || len(res.Servers) != 1 || res.Servers[0].Command != "just" {
		t.Fatalf("result %+v", res)
	}
	got := readLocalSettings(t, dir)
	if !reflect.DeepEqual(got["disabledMcpjsonServers"], []any{"lib"}) {
		t.Fatalf("settings %v", got)
	}
	if err := claudeMCPUndo(dir, protocol.MCPUndo{Choice: protocol.MCPDisable, Added: res.Added}); err != nil {
		t.Fatal(err)
	}
	if v := readLocalSettings(t, dir)["disabledMcpjsonServers"]; v != nil && len(v.([]any)) != 0 {
		t.Fatalf("undo left %v", v)
	}
}

func TestClaudeMCPChooseRefusesAFolderWithoutServers(t *testing.T) {
	dir := t.TempDir()
	if _, err := claudeMCPChoose(dir, protocol.MCPApprove); err == nil || !strings.Contains(err.Error(), ".mcp.json") {
		t.Fatalf("want a clear error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Fatal("nothing may be written")
	}
	writeMCPJSON(t, dir, map[string]any{"a": map[string]any{"command": "x"}})
	if _, err := claudeMCPChoose(dir, "all"); err == nil {
		t.Fatal("an unknown choice must be refused")
	}
}

func TestClaudeJobMCPApprovalDetail(t *testing.T) {
	for detail, want := range map[string]bool{
		"1 new MCP server needs approval":  true,
		"3 new MCP servers need approval":  true,
		"waiting for the user to continue": false,
		"stopped":                          false,
	} {
		if got := claudeMCPApprovalDetail(detail); got != want {
			t.Errorf("%q: got %t want %t", detail, got, want)
		}
	}
}
