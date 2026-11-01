package inventory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/pelletier/go-toml/v2"
)

func writeProjectMCP(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanMarksSameAndDifferentMCPDefinitions(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	writeProjectMCP(t, project, ".mcp.json", `{"mcpServers":{"homelab-mcp":{"type":"stdio","command":"homelab-mcp","args":["--stdio"],"env":{"HOMELAB_TOKEN":"${HOMELAB_TOKEN}"}}}}`)
	writeProjectMCP(t, project, ".pi/mcp.json", `{"mcpServers":{"same":{"command":"same"},"other":{"command":"one"}}}`)
	writeProjectMCP(t, project, "opencode.json", `{"mcp":{"same":{"type":"local","command":["same"]},"other":{"type":"local","command":["two"]}}}`)

	report := Scan(Options{Home: home, CodexHome: filepath.Join(home, ".codex"), Projects: []protocol.ProjectEntry{{Name: "homelab", Path: project}}, TrustedProjects: map[string]bool{project: true}})
	defs := map[string]string{}
	for _, item := range report.Items {
		if item.Kind == protocol.InventoryMCP && item.Scope == protocol.InventoryProject {
			if item.Definition == "" {
				t.Fatalf("project MCP entry %s on %s has no definition: %+v", item.Name, item.Harness, item)
			}
			defs[item.Name+"\x00"+item.Harness] = item.Definition
		}
	}
	if defs["same\x00pi"] == "" || defs["same\x00opencode"] == "" {
		t.Fatalf("missing same entry: %+v", defs)
	}
	if defs["same\x00pi"] != defs["same\x00opencode"] {
		t.Fatalf("equal definitions got different digests: %q %q", defs["same\x00pi"], defs["same\x00opencode"])
	}
	if defs["other\x00pi"] == defs["other\x00opencode"] {
		t.Fatalf("different definitions got the same digest: %q", defs["other\x00pi"])
	}
	if defs["homelab-mcp\x00claude"] == "" {
		t.Fatalf("missing claude project server: %+v", defs)
	}
}

func TestProjectMCPDescriptorWritesOpenCodeWithEnvReferences(t *testing.T) {
	source := map[string]any{"type": "stdio", "command": "homelab-mcp", "args": []any{"--stdio"}, "env": map[string]any{"HOMELAB_TOKEN": "${HOMELAB_TOKEN}"}}
	desc, err := ProjectMCPDescriptor("opencode", source)
	if err != nil {
		t.Fatal(err)
	}
	if desc["type"] != "local" {
		t.Fatalf("opencode transport = %v", desc["type"])
	}
	cmd, _ := desc["command"].([]any)
	if len(cmd) != 2 || cmd[0] != "homelab-mcp" || cmd[1] != "--stdio" {
		t.Fatalf("opencode command = %#v", desc["command"])
	}
	env, _ := desc["environment"].(map[string]any)
	if env["HOMELAB_TOKEN"] != "${HOMELAB_TOKEN}" {
		t.Fatalf("environment reference was lost: %#v", desc)
	}
	if _, ok := desc["env"]; ok {
		t.Fatalf("opencode shape kept the claude env key: %#v", desc)
	}
}

func TestAddProjectMCPEntryWritesHarnessFormatsAndKeepsReferences(t *testing.T) {
	root := t.TempDir()
	source := map[string]any{"type": "stdio", "command": "homelab-mcp", "args": []any{"--stdio"}, "env": map[string]any{"HOMELAB_TOKEN": "${HOMELAB_TOKEN}"}}

	for _, harness := range []string{"claude", "pi", "opencode"} {
		path, key, tomlFormat, err := ProjectMCPFile(harness, root)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := ProjectMCPDescriptor(harness, source)
		if err != nil {
			t.Fatal(err)
		}
		out, err := AddProjectMCPEntry(root, path, key, tomlFormat, "homelab-mcp", desc, nil)
		if err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		if len(out) == 0 {
			t.Fatalf("%s: empty output", harness)
		}
		var doc map[string]any
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("%s: invalid JSON: %v\n%s", harness, err, out)
		}
		holder, _ := doc[key].(map[string]any)
		entry, _ := holder["homelab-mcp"].(map[string]any)
		if entry == nil {
			t.Fatalf("%s: entry missing:\n%s", harness, out)
		}
		env := entry["env"]
		if harness == "opencode" {
			env = entry["environment"]
		}
		m, _ := env.(map[string]any)
		if m["HOMELAB_TOKEN"] != "${HOMELAB_TOKEN}" {
			t.Fatalf("%s: env reference changed: %#v", harness, entry)
		}
		if string(out) == "" || !json.Valid(out) {
			t.Fatalf("%s: invalid output", harness)
		}
	}
}

func TestAddProjectMCPEntryPreservesOtherContent(t *testing.T) {
	root := t.TempDir()
	path := writeProjectMCP(t, root, "opencode.json", "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"other\": {\"type\": \"local\", \"command\": [\"other\"]}\n  }\n}\n")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := ProjectMCPDescriptor("opencode", map[string]any{"command": "homelab-mcp"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := AddProjectMCPEntry(root, path, "mcp", false, "homelab-mcp", desc, original)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if doc["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("other top-level content was lost: %#v", doc)
	}
	mcp, _ := doc["mcp"].(map[string]any)
	if mcp["other"] == nil || mcp["homelab-mcp"] == nil {
		t.Fatalf("entries after the edit: %#v", mcp)
	}
	if len(out) < len(original) {
		t.Fatalf("the edit shrank the file")
	}
}

func TestProjectMCPDescriptorRefusesLiteralEnvValue(t *testing.T) {
	if _, err := ProjectMCPDescriptor("opencode", map[string]any{"command": "x", "env": map[string]any{"TOKEN": "literal-secret"}}); err == nil {
		t.Fatal("a literal environment value must be refused")
	}
}

func TestAddProjectMCPEntryWritesCodexTOML(t *testing.T) {
	root := t.TempDir()
	path, key, tomlFormat, err := ProjectMCPFile("codex", root)
	if err != nil {
		t.Fatal(err)
	}
	if !tomlFormat || key != "mcp_servers" {
		t.Fatalf("codex config = %q %q %v", path, key, tomlFormat)
	}
	desc, err := ProjectMCPDescriptor("codex", map[string]any{"command": "homelab-mcp", "args": []any{"--stdio"}, "env": map[string]any{"HOMELAB_TOKEN": "${HOMELAB_TOKEN}"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := AddProjectMCPEntry(root, path, key, tomlFormat, "homelab-mcp", desc, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, out)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	entry, _ := servers["homelab-mcp"].(map[string]any)
	if entry["command"] != "homelab-mcp" {
		t.Fatalf("codex entry = %#v\n%s", entry, out)
	}
	env, _ := entry["env"].(map[string]any)
	if env["HOMELAB_TOKEN"] != "${HOMELAB_TOKEN}" {
		t.Fatalf("codex env reference lost: %#v\n%s", entry, out)
	}
}

func TestAddProjectMCPEntryRefusesSameName(t *testing.T) {
	root := t.TempDir()
	path := writeProjectMCP(t, root, ".pi/mcp.json", `{"mcpServers":{"homelab-mcp":{"command":"already"}}}`)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	desc, _ := ProjectMCPDescriptor("pi", map[string]any{"command": "homelab-mcp"})
	if _, err := AddProjectMCPEntry(root, path, "mcpServers", false, "homelab-mcp", desc, data); !errors.Is(err, os.ErrExist) {
		t.Fatalf("same-name entry must be refused, got %v", err)
	}
}
