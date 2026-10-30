package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func TestDescriptorFingerprintDistinguishesNonsecretInputs(t *testing.T) {
	a := map[string]any{"command": "node", "args": []any{"--port", "3000", "--token", "secret-one"}, "env": map[string]any{"TOKEN": "secret-one"}}
	b := map[string]any{"command": "python", "args": []any{"--port", "3000", "--token", "secret-one"}, "env": map[string]any{"TOKEN": "secret-one"}}
	c := map[string]any{"command": "node", "args": []any{"--port", "3000", "--token", "secret-two"}, "env": map[string]any{"TOKEN": "secret-two"}}
	if fingerprint("server", a) == fingerprint("server", b) {
		t.Fatal("command ignored")
	}
	if fingerprint("server", a) != fingerprint("server", c) {
		t.Fatal("secret affected fingerprint")
	}
	if fingerprint("server", a) == fingerprint("other", a) {
		t.Fatal("name ignored")
	}
}

func TestEveryHarnessReportsInvalidConfigAndEscapedSkills(t *testing.T) {
	cases := []struct{ h, config, skills string }{
		{"claude", ".claude.json", ".claude/skills"},
		{"codex", ".codex/config.toml", ".agents/skills"},
		{"opencode", ".config/opencode/opencode.json", ".config/opencode/skills"},
		{"pi", ".pi/agent/mcp.json", ".pi/agent/skills"},
	}
	for _, c := range cases {
		t.Run(c.h, func(t *testing.T) {
			home := t.TempDir()
			cp := filepath.Join(home, c.config)
			if err := os.MkdirAll(filepath.Dir(cp), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cp, []byte("{invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			sp := filepath.Join(home, c.skills)
			if err := os.MkdirAll(filepath.Dir(sp), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), sp); err != nil {
				t.Fatal(err)
			}
			report := Scan(Options{Home: home})
			var configError, skillError bool
			for _, item := range report.Items {
				if item.Harness == c.h && item.State == protocol.InventoryError {
					if item.Kind == protocol.InventoryMCP {
						configError = true
					}
					if item.Kind == protocol.InventorySkill {
						skillError = true
					}
				}
			}
			if !configError || !skillError {
				t.Fatalf("missing errors: config %v skill %v", configError, skillError)
			}
		})
	}
}

func TestScanFailureMarksReportError(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := Scan(Options{Home: home}); r.State != protocol.InventoryError {
		t.Fatalf("state = %q", r.State)
	}
}

func TestEveryHarnessReportsOversizedConfig(t *testing.T) {
	cases := []struct{ h, config string }{{"claude", ".claude.json"}, {"codex", ".codex/config.toml"}, {"opencode", ".config/opencode/opencode.json"}, {"pi", ".pi/agent/mcp.json"}}
	for _, c := range cases {
		t.Run(c.h, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, c.config)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(1<<20 + 1); err != nil {
				t.Fatal(err)
			}
			f.Close()
			r := Scan(Options{Home: home})
			for _, item := range r.Items {
				if item.Harness == c.h && item.Kind == protocol.InventoryMCP && item.State == protocol.InventoryError {
					return
				}
			}
			t.Fatal("missing oversize error")
		})
	}
}

func TestPiExtensionsReportedManual(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".pi/agent/extensions")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home})
	for _, item := range report.Items {
		if item.Harness == "pi" && item.Kind == protocol.InventoryMCP && len(item.Unsupported) > 0 && item.Source == protocol.InventoryPlugin {
			return
		}
	}
	t.Fatal("missing extension warning")
}

func TestHarnessProjectTrust(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	files := map[string]string{
		filepath.Join(home, ".codex/config.toml"):    "[projects." + strconv.Quote(project) + "]\ntrust_level = 'trusted'\n",
		filepath.Join(home, ".pi/agent/trust.json"):  fmt.Sprintf("{%q:true}", project),
		filepath.Join(project, ".codex/config.toml"): "[mcp_servers.codex-local]\ncommand='node'\n",
		filepath.Join(project, ".pi/mcp.json"):       `{"mcpServers":{"pi-local":{"command":"node"}}}`,
	}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := Scan(Options{Home: home, Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}})
	seen := map[string]bool{}
	for _, item := range report.Items {
		seen[item.Harness+":"+item.Name] = true
	}
	if !seen["codex:codex-local"] || !seen["pi:pi-local"] {
		t.Fatalf("missing trusted project MCP: %+v", report.Items)
	}
}

func TestProjectSkillAndMCPRoots(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	files := map[string]string{
		".claude/skills/a/SKILL.md": "a", ".agents/skills/b/SKILL.md": "b", ".opencode/skills/c/SKILL.md": "c", ".pi/skills/d/SKILL.md": "d",
		".mcp.json":          `{"mcpServers":{"claude-local":{"command":"node"}}}`,
		".codex/config.toml": "[mcp_servers.codex-local]\ncommand='node'\n",
		"opencode.jsonc":     `{"mcp":{"open-local":{"command":["node"]}}}`,
		".pi/mcp.json":       `{"mcpServers":{"pi-local":{"command":"node"}}}`,
	}
	for name, data := range files {
		path := filepath.Join(project, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := Scan(Options{Home: home, Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}, TrustedProjects: map[string]bool{project: true}})
	found := map[string]bool{}
	for _, v := range r.Items {
		if v.Scope == protocol.InventoryProject && v.Project == "p" {
			found[v.Harness+":"+v.Name] = true
		}
	}
	for _, key := range []string{"claude:a", "codex:b", "opencode:c", "pi:d", "claude:claude-local", "codex:codex-local", "opencode:open-local", "pi:pi-local"} {
		if !found[key] {
			t.Errorf("missing %s", key)
		}
	}
}

func TestCodexSystemSkillProvenance(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex/skills/.system/standard")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("system"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range Scan(Options{Home: home}).Items {
		if item.Harness == "codex" && item.Name == "standard" && item.Source == protocol.InventorySystem {
			return
		}
	}
	t.Fatal("missing system skill")
}

func TestPiSettingsSingleSkillFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".pi/agent/extra/one")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi/agent/settings.json"), []byte(`{"skills":["extra/one/SKILL.md"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home})
	for _, item := range report.Items {
		if item.Harness == "pi" && item.Name == "one" && item.State == protocol.InventoryOK {
			return
		}
	}
	t.Fatalf("missing skill: %+v", report.Items)
}

func TestInventoryLimitsOversizedNames(t *testing.T) {
	home := t.TempDir()
	doc := map[string]any{"mcpServers": map[string]any{strings.Repeat("x", 5000): map[string]any{"command": "node"}}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	r := Scan(Options{Home: home})
	if r.State != protocol.InventoryError {
		t.Fatal("oversized name accepted")
	}
	for _, item := range r.Items {
		if len(item.Name) > 4096 {
			t.Fatal("oversized name reported")
		}
	}
}
