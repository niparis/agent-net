package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestHarnessInventory(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	files := map[string]string{
		".claude/skills/a/SKILL.md": "hello", ".agents/skills/b/SKILL.md": "hello", ".config/opencode/skills/c/SKILL.md": "hello", ".pi/agent/skills/d/SKILL.md": "hello",
		".claude.json":                    `{"mcpServers":{"claude-server":{"command":"node","env":{"TOKEN":"secret-value"}}}}`,
		".codex/config.toml":              "[mcp_servers.codex-server]\ncommand = 'node'\nenv = { TOKEN = 'secret-value' }\n",
		".config/opencode/opencode.jsonc": "{ // hi\n\"mcp\": {\"open-server\": {\"command\": [\"node\", \"secret-value\"]}}}",
		".pi/agent/mcp.json":              `{"mcpServers":{"pi-server":{"url":"https://example.test/?token=secret-value"}}}`,
	}
	for name, data := range files {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := Scan(Options{Home: home, CodexHome: filepath.Join(home, ".codex"), Machine: "machine", Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}})
	found := map[string]bool{}
	for _, item := range r.Items {
		found[item.Harness+":"+item.Name] = true
		if strings.Contains(item.Hash, "secret-value") || strings.Contains(item.Reason, "secret-value") {
			t.Fatal("secret leaked")
		}
	}
	for _, want := range []string{"claude:a", "codex:b", "opencode:c", "pi:d", "claude:claude-server", "codex:codex-server", "opencode:open-server", "pi:pi-server"} {
		if !found[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestMalformedAndSymlinkSources(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{invalid secret-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home, CodexHome: filepath.Join(home, ".codex")})
	count := 0
	for _, item := range report.Items {
		if item.State == protocol.InventoryError {
			count++
			if strings.Contains(item.Reason, "secret-value") {
				t.Fatal("secret leaked")
			}
		}
	}
	if count < 2 {
		t.Fatalf("missing errors: %+v", report.Items)
	}
}

func TestClaudeProjectLocalMCP(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	doc := map[string]any{"projects": map[string]any{project: map[string]any{"mcpServers": map[string]any{"local-server": map[string]any{"command": "run"}}}}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	r := Scan(Options{Home: home, Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}})
	for _, item := range r.Items {
		if item.Harness == "claude" && item.Name == "local-server" && item.Scope == protocol.InventoryProject && item.Project == "p" {
			return
		}
	}
	t.Fatal("missing project-local Claude server")
}

func TestJSONCCommentsAndTrailingCommas(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(stripComments([]byte("{\"text\":\"https://example.test\",/* comment */\"list\":[1,2,],}")), &doc); err != nil {
		t.Fatal(err)
	}
}

func TestMCPFingerprintDoesNotDependOnSecretValues(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	fingerprint := func(value string) string {
		doc := map[string]any{"mcpServers": map[string]any{"server": map[string]any{"env": map[string]string{"TOKEN": value}, "args": []string{value}}}}
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		for _, item := range Scan(Options{Home: home}).Items {
			if item.Harness == "claude" && item.Name == "server" {
				return item.Hash
			}
		}
		t.Fatal("missing server")
		return ""
	}
	if a, b := fingerprint("first-secret"), fingerprint("second-secret"); a == "" || a != b {
		t.Fatalf("secret affected hash: %q %q", a, b)
	}
}

func TestPiSettingsSkillPaths(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".pi/agent")
	if err := os.MkdirAll(filepath.Join(dir, "extras", "custom"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extras", "custom", "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"skills":["extras"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range Scan(Options{Home: home}).Items {
		if item.Harness == "pi" && item.Name == "custom" {
			return
		}
	}
	t.Fatal("missing configured skill path")
}
