package inventory

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func hashTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out[path] = sha256.Sum256(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// verdicts scans the fixture and maps "harness:scope:name" to its import info.
func verdicts(t *testing.T, o Options) map[string]*protocol.MCPImportInfo {
	t.Helper()
	out := map[string]*protocol.MCPImportInfo{}
	for _, item := range Scan(o).Items {
		if item.Kind == protocol.InventoryMCP && item.Name != "" && item.Import != nil {
			out[item.Harness+":"+item.Scope+":"+item.Name] = item.Import
		}
	}
	return out
}

func TestMCPImportClassification(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	const secret = "s3cr3t-value-9f2"
	writeFiles(t, home, map[string]string{
		".claude.json": `{"mcpServers":{
			"files":{"command":"/usr/bin/files-mcp","args":["--root","/srv/data"],"env":{"API_TOKEN":"${API_TOKEN}"}},
			"remote":{"type":"http","url":"https://mcp.example.test/v1","headers":{"X-Api":"${REMOTE_KEY}"}},
			"inline":{"command":"/usr/bin/x","env":{"API_TOKEN":"` + secret + `"}},
			"signin":{"type":"http","url":"https://mcp.example.test/oauth","oauth":{"clientId":"abc"}},
			"relative":{"command":"node","args":["./dist/server.js"]},
			"argsecret":{"command":"/usr/bin/x","args":["--token","` + secret + `"]},
			"query":{"type":"sse","url":"https://mcp.example.test/sse?api_key=` + secret + `"},
			"weird":{"type":"carrier-pigeon","command":"x"},
			"prompts":{"command":"/usr/bin/p","capabilities":{"prompts":true}}}}`,
		".codex/config.toml": "[mcp_servers.cx]\ncommand = '/usr/bin/cx'\nargs = ['--serve']\nenv_vars = ['CX_TOKEN']\nenabled_tools = ['alpha','beta']\n" +
			"[mcp_servers.cxweb]\nurl = 'https://mcp.example.test/cx'\nenv_http_headers = { Authorization = 'CX_AUTH' }\n" +
			"[mcp_servers.cxbearer]\nurl = 'https://mcp.example.test/b'\nbearer_token_env_var = 'CX_BEARER'\n" +
			"[mcp_servers.cxinline]\nurl = 'https://mcp.example.test/i'\nhttp_headers = { Authorization = 'Bearer " + secret + "' }\n",
		".pi/agent/mcp.json": `{"mcpServers":{"pi-ok":{"command":"/usr/bin/pi-mcp"},"pi-auth":{"url":"https://mcp.example.test/p","auth":"oauth"}}}`,
		".config/opencode/opencode.json": `{"mcp":{
			"oc-local":{"type":"local","command":["/usr/bin/oc","--stdio"],"environment":{"OC_KEY":"${OC_KEY}"}},
			"oc-remote":{"type":"remote","url":"https://mcp.example.test/oc","oauth":{}},
			"oc-off":{"type":"local","command":["/usr/bin/off"],"enabled":false}}}`,
	})
	writeFiles(t, project, map[string]string{
		".mcp.json": `{"mcpServers":{"proj":{"command":"/usr/bin/proj"},"proj-expand":{"command":"${CLAUDE_PROJECT_DIR}/run.sh"}}}`,
	})
	o := Options{Home: home, CodexHome: filepath.Join(home, ".codex"), Machine: "m", Projects: []protocol.ProjectEntry{{Name: "p", Path: project}}}
	before := hashTree(t, home)
	beforeProject := hashTree(t, project)
	got := verdicts(t, o)

	importable := []string{"claude:user:files", "claude:user:remote", "codex:user:cx", "codex:user:cxweb", "pi:user:pi-ok", "opencode:user:oc-local", "claude:project:proj"}
	for _, key := range importable {
		if v := got[key]; v == nil || v.Class != protocol.MCPImportable {
			t.Errorf("%s should be importable: %+v", key, v)
		}
	}
	unsupported := map[string]string{
		"claude:user:inline":         "inline",
		"claude:user:signin":         "OAuth",
		"claude:user:relative":       "working directory",
		"claude:user:argsecret":      "secret value",
		"claude:user:query":          "credential",
		"claude:user:weird":          "transport",
		"claude:user:prompts":        "resources or prompts",
		"codex:user:cxbearer":        "bearer",
		"codex:user:cxinline":        "inline",
		"pi:user:pi-auth":            "OAuth",
		"opencode:user:oc-remote":    "OAuth",
		"opencode:user:oc-off":       "switched off",
		"claude:project:proj-expand": "expansion",
	}
	for key, want := range unsupported {
		v := got[key]
		if v == nil || v.Class != protocol.MCPUnsupported || !strings.Contains(v.Reason, want) {
			t.Errorf("%s should be unsupported with %q: %+v", key, want, v)
		}
	}
	if v := got["codex:user:cx"]; v != nil && (strings.Join(v.ExpectedTools, ",") != "alpha,beta" || strings.Join(v.EnvNames, ",") != "CX_TOKEN") {
		t.Errorf("codex facts: %+v", v)
	}
	if v := got["claude:user:remote"]; v != nil && (v.Transport != protocol.MCPHTTP || strings.Join(v.EnvNames, ",") != "REMOTE_KEY") {
		t.Errorf("remote facts: %+v", v)
	}

	raw, _ := json.Marshal(Scan(o))
	if strings.Contains(string(raw), secret) {
		t.Fatal("a secret value reached the report")
	}
	// The scan and the verdicts are read-only.
	for path, sum := range before {
		if hashTree(t, home)[path] != sum {
			t.Errorf("%s changed", path)
		}
	}
	for path, sum := range beforeProject {
		if hashTree(t, project)[path] != sum {
			t.Errorf("%s changed", path)
		}
	}
}

func TestLoadNativeMCPReturnsServerWithoutValues(t *testing.T) {
	home := t.TempDir()
	writeFiles(t, home, map[string]string{
		".codex/config.toml": "[mcp_servers.cx]\ncommand = '/usr/bin/cx'\nargs = ['--serve']\nenv_vars = ['CX_TOKEN']\n[mcp_servers.web]\nurl = 'https://mcp.example.test/w'\nenv_http_headers = { Authorization = 'CX_AUTH' }\n",
	})
	o := Options{Home: home, CodexHome: filepath.Join(home, ".codex")}
	loc := filepath.Join(home, ".codex/config.toml")
	info, srv, hash, err := LoadNativeMCP(o, "codex", protocol.InventoryUser, "", loc, "cx")
	if err != nil || info.Class != protocol.MCPImportable || hash == "" {
		t.Fatalf("%+v %v", info, err)
	}
	if srv.Name != "cx" || srv.Transport != protocol.MCPStdio || srv.Command != "/usr/bin/cx" || strings.Join(srv.Env, ",") != "CX_TOKEN" {
		t.Fatalf("server: %+v", srv)
	}
	_, web, _, err := LoadNativeMCP(o, "codex", protocol.InventoryUser, "", loc, "web")
	if err != nil || len(web.Headers) != 1 || web.Headers[0].Env != "CX_AUTH" {
		t.Fatalf("web: %+v %v", web, err)
	}
	if _, _, _, err = LoadNativeMCP(o, "codex", protocol.InventoryUser, "", filepath.Join(home, "other.toml"), "cx"); err == nil {
		t.Fatal("an unknown source was read")
	}
	if _, _, _, err = LoadNativeMCP(o, "codex", protocol.InventoryUser, "", loc, "gone"); err == nil {
		t.Fatal("a missing entry was found")
	}
}

func TestMCPServerName(t *testing.T) {
	for in, want := range map[string]string{"files": "files", "My_Server.v2": "my-server-v2", "UPPER": "upper", "___": ""} {
		if got := protocol.MCPServerName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// A plain ${VAR} reference becomes the variable NAME on the federated server.
// The value stays in the host's own environment and is never read here.
func TestMCPSecretSourceConversionKeepsOnlyNames(t *testing.T) {
	const secret = "value-that-must-never-move-7731"
	t.Setenv("API_TOKEN", secret)
	t.Setenv("REMOTE_KEY", secret)
	info, server := ClassifyMCP("files", map[string]any{"command": "/usr/bin/files-mcp", "env": map[string]any{"API_TOKEN": "${API_TOKEN}"}})
	if info.Class != protocol.MCPImportable || len(server.Env) != 1 || server.Env[0] != "API_TOKEN" {
		t.Fatalf("env reference: %+v %+v", info, server)
	}
	info, server = ClassifyMCP("remote", map[string]any{"type": "http", "url": "https://mcp.example.test/v1", "headers": map[string]any{"X-Api": "${REMOTE_KEY}"}})
	if info.Class != protocol.MCPImportable || len(server.Headers) != 1 || server.Headers[0].Env != "REMOTE_KEY" {
		t.Fatalf("header reference: %+v %+v", info, server)
	}
	for _, v := range []any{info, server} {
		if b, _ := json.Marshal(v); strings.Contains(string(b), secret) {
			t.Fatalf("a value reached the verdict or the server: %s", b)
		}
	}
	// A reference to another variable, or a literal, stays unmanaged.
	for _, env := range []any{"${OTHER}", secret} {
		if info, _ := ClassifyMCP("x", map[string]any{"command": "/usr/bin/x", "env": map[string]any{"API_TOKEN": env}}); info.Class != protocol.MCPUnsupported {
			t.Fatalf("%v was accepted", env)
		}
	}
}
