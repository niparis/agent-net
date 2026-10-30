package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	if fingerprint("server", a) != fingerprint("other", a) {
		t.Fatal("name affected descriptor fingerprint")
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

func TestPiDefaultProjectTrust(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	files := map[string]string{filepath.Join(home, ".pi/agent/settings.json"): `{"defaultProjectTrust":"always"}`, filepath.Join(project, ".pi/mcp.json"): `{"mcpServers":{"local":{"command":"node"}}}`}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range Scan(Options{Home: home, Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}}).Items {
		if v.Harness == "pi" && v.Name == "local" {
			return
		}
	}
	t.Fatal("default trust ignored")
}

func TestInvalidServersFieldReportsError(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home})
	if report.State != protocol.InventoryError {
		t.Fatal("invalid server map accepted")
	}
}

func TestSkillManifestAllowsInTreeSkillLink(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/linked")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("body.md", filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, item := range Scan(Options{Home: home}).Items {
		if item.Harness == "claude" && item.Name == "linked" && item.State == protocol.InventoryOK {
			return
		}
	}
	t.Fatal("missing skill with in-tree SKILL.md link")
}

func TestDescriptorMasksUnknownFlagValues(t *testing.T) {
	a := map[string]any{"command": "node", "args": []any{"--secret-one"}}
	b := map[string]any{"command": "node", "args": []any{"--secret-two"}}
	if fingerprint("server", a) != fingerprint("server", b) {
		t.Fatal("unknown flag value affected fingerprint")
	}
}

func TestDescriptorIncludesSafePortArgument(t *testing.T) {
	a := map[string]any{"command": "node", "args": []any{"--port", "3000"}}
	b := map[string]any{"command": "node", "args": []any{"--port", "4000"}}
	if fingerprint("server", a) == fingerprint("server", b) {
		t.Fatal("safe port ignored")
	}
}

func TestPiSettingsSingleSkillDirectory(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".pi/agent/extra/one")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi/agent/settings.json"), []byte(`{"skills":["extra/one"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range Scan(Options{Home: home}).Items {
		if item.Harness == "pi" && item.Name == "one" && item.State == protocol.InventoryOK {
			return
		}
	}
	t.Fatal("missing configured skill directory")
}

func TestClaudePluginSourceReportedManual(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude/plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, item := range Scan(Options{Home: home}).Items {
		if item.Harness == "claude" && item.Source == protocol.InventoryPlugin && len(item.Unsupported) > 0 {
			return
		}
	}
	t.Fatal("missing plugin warning")
}

func TestPiPackageSourceReportedManual(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".pi/agent/settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"packages":["npm:some-package"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range Scan(Options{Home: home}).Items {
		if item.Harness == "pi" && item.Source == protocol.InventoryPackage && len(item.Unsupported) > 0 {
			return
		}
	}
	t.Fatal("missing package warning")
}

func TestPiProjectExtensionsReportedManual(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".pi/extensions"), 0700); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home, Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}, TrustedProjects: map[string]bool{project: true}})
	for _, item := range report.Items {
		if item.Harness == "pi" && item.Scope == protocol.InventoryProject && item.Source == protocol.InventoryPlugin && len(item.Unsupported) > 0 {
			return
		}
	}
	t.Fatal("missing project extension warning")
}

func TestPiCompatibleAndNestedSkillRoots(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	files := []string{filepath.Join(home, ".agents/skills/compatible/SKILL.md"), filepath.Join(home, ".pi/agent/skills/group/nested/SKILL.md"), filepath.Join(project, ".agents/skills/project-compatible/SKILL.md")}
	for _, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("skill"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	found := map[string]bool{}
	for _, item := range Scan(Options{Home: home, Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}}).Items {
		if item.Harness == "pi" {
			found[item.Name] = true
		}
	}
	for _, name := range []string{"compatible", "nested", "project-compatible"} {
		if !found[name] {
			t.Errorf("missing %s", name)
		}
	}
}

func TestMCPFingerprintIncludesURLPathAndEnvironmentNames(t *testing.T) {
	base := map[string]any{"url": "https://example.invalid/first?token=secret-one", "env": map[string]any{"API_KEY": "secret-one"}}
	otherPath := map[string]any{"url": "https://example.invalid/second?token=secret-two", "env": map[string]any{"API_KEY": "secret-two"}}
	otherEnv := map[string]any{"url": "https://example.invalid/first?token=secret-two", "env": map[string]any{"OTHER_KEY": "secret-two"}}
	same := map[string]any{"url": "https://example.invalid/first?token=secret-two", "env": map[string]any{"API_KEY": "secret-two"}}
	if fingerprint("server", base) == fingerprint("server", otherPath) || fingerprint("server", base) == fingerprint("server", otherEnv) {
		t.Fatal("URL path or environment variable names omitted from fingerprint")
	}
	if fingerprint("server", base) != fingerprint("server", same) {
		t.Fatal("secret values changed fingerprint")
	}
}

func TestMCPFingerprintMasksSecretArguments(t *testing.T) {
	desc := func(path, token string) map[string]any {
		return map[string]any{"command": "node", "args": []any{"server.js", "--config", path, "--token", token}}
	}
	if fingerprint("s", desc("one.json", "secret-one")) != fingerprint("s", desc("one.json", "secret-two")) {
		t.Fatal("secret changed fingerprint")
	}
	if fingerprint("s", desc("one.json", "secret-one")) == fingerprint("s", desc("two.json", "secret-one")) {
		t.Fatal("non-secret argument omitted")
	}
}

func TestFederationItemRedactsRegistryValues(t *testing.T) {
	s := protocol.MCPServer{Name: "demo", Transport: "stdio", Command: "node", Args: []string{"--token", "fixture-secret"}, Env: []string{"API_KEY"}}
	item := FederationItem(s, time.Time{})
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if item.Source != protocol.InventoryFederation || item.Hash == "" || strings.Contains(string(b), "fixture-secret") {
		t.Fatalf("unsafe federation item: %s", b)
	}
	s.Args[1] = "another-secret"
	if FederationItem(s, time.Time{}).Hash != item.Hash {
		t.Fatal("secret changes hash")
	}
}

func TestSkillFileListsRespectReportLimit(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1400; i++ {
		name := fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 190))
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := Scan(Options{Home: home})
	if report.State != protocol.InventoryError {
		t.Fatalf("large file list accepted: %s", report.State)
	}
}

func TestMCPFingerprintIncludesCommandPath(t *testing.T) {
	a := map[string]any{"command": "/opt/one/server"}
	b := map[string]any{"command": "/opt/two/server"}
	if fingerprint("demo", a) == fingerprint("demo", b) {
		t.Fatal("command paths collapsed")
	}
}

func TestMCPFingerprintIncludesSafeScriptAndMode(t *testing.T) {
	desc := func(script, mode, secret string) map[string]any {
		return map[string]any{"command": "node", "args": []any{script, "--mode", mode, "--api-key", secret}}
	}
	base := fingerprint("demo", desc("first.js", "read", "fixture-secret-one"))
	if base == fingerprint("demo", desc("second.js", "read", "fixture-secret-one")) || base == fingerprint("demo", desc("first.js", "write", "fixture-secret-one")) {
		t.Fatal("safe arguments were omitted")
	}
	if base != fingerprint("demo", desc("first.js", "read", "fixture-secret-two")) {
		t.Fatal("secret changed fingerprint")
	}
}
