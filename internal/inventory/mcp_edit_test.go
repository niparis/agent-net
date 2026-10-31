package inventory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func removeVia(t *testing.T, harness, scope, root, rel, in, entry string) ([]byte, error) {
	t.Helper()
	home := t.TempDir()
	o := Options{Home: home}
	path := filepath.Join(home, rel)
	if scope == "project" && rel != ".claude.json" {
		path = filepath.Join(root, rel)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if scope == "project" && rel == ".claude.json" {
		// The project-local entries of ~/.claude.json belong to a project root.
		return RemoveNativeEntry(o, harness, scope, root, path, entry, []byte(in))
	}
	return RemoveNativeEntry(o, harness, scope, root, path, entry, []byte(in))
}

func TestRemoveNativeEntryKeepsEverythingElse(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name, harness, scope, rel, in, entry, want string
	}{
		{
			name: "claude user two-space middle", harness: "claude", scope: "user", rel: ".claude.json", entry: "b",
			in:   "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"},\n    \"b\": {\n      \"command\": \"y\",\n      \"args\": [\"1\"]\n    },\n    \"c\": {\"command\": \"z\"}\n  },\n  \"theme\": \"dark\"\n}\n",
			want: "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"},\n    \"c\": {\"command\": \"z\"}\n  },\n  \"theme\": \"dark\"\n}\n",
		},
		{
			name: "claude user tabs last entry", harness: "claude", scope: "user", rel: ".claude.json", entry: "c",
			in:   "{\n\t\"mcpServers\": {\n\t\t\"a\": {\"command\": \"x\"},\n\t\t\"c\": {\"command\": \"z\"}\n\t},\n\t\"k\": [1, 2]\n}",
			want: "{\n\t\"mcpServers\": {\n\t\t\"a\": {\"command\": \"x\"}\n\t},\n\t\"k\": [1, 2]\n}",
		},
		{
			name: "claude user only entry odd spaces", harness: "claude", scope: "user", rel: ".claude.json", entry: "a",
			in:   "{   \"mcpServers\" :{ \"a\" : {\"command\":\"x\"} } ,   \"z\":1}",
			want: "{   \"mcpServers\" :{  } ,   \"z\":1}",
		},
		{
			name: "claude project-local entry", harness: "claude", scope: "project", rel: ".claude.json", entry: "p",
			in:   "{\n    \"projects\": {\n        \"ROOT\": {\n            \"allowedTools\": [],\n            \"mcpServers\": {\n                \"p\": {\"command\": \"x\"},\n                \"q\": {\"command\": \"y\"}\n            }\n        },\n        \"/other\": {\"mcpServers\": {\"p\": {\"command\": \"keep\"}}}\n    }\n}\n",
			want: "{\n    \"projects\": {\n        \"ROOT\": {\n            \"allowedTools\": [],\n            \"mcpServers\": {\n                \"q\": {\"command\": \"y\"}\n            }\n        },\n        \"/other\": {\"mcpServers\": {\"p\": {\"command\": \"keep\"}}}\n    }\n}\n",
		},
		{
			name: "pi mcp.json", harness: "pi", scope: "user", rel: ".pi/agent/mcp.json", entry: "a",
			in:   "{\n  \"settings\": {\"x\": 1},\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"},\n    \"b\": {\"url\": \"https://e.example/mcp\"}\n  }\n}\n",
			want: "{\n  \"settings\": {\"x\": 1},\n  \"mcpServers\": {\n    \"b\": {\"url\": \"https://e.example/mcp\"}\n  }\n}\n",
		},
		{
			name: "opencode jsonc comments and trailing commas", harness: "opencode", scope: "user", rel: ".config/opencode/opencode.jsonc", entry: "a",
			in:   "{\n  // my servers\n  \"mcp\": {\n    /* first */\n    \"a\": {\"type\": \"local\", \"command\": [\"x\"]}, // about a\n    // about b\n    \"b\": {\"type\": \"remote\", \"url\": \"https://e.example/mcp\"},\n  },\n  \"theme\": \"x\", // end\n}\n",
			want: "{\n  // my servers\n  \"mcp\": {\n    /* first */\n    // about a\n    // about b\n    \"b\": {\"type\": \"remote\", \"url\": \"https://e.example/mcp\"},\n  },\n  \"theme\": \"x\", // end\n}\n",
		},
		{
			name: "key and string contents that look like json", harness: "claude", scope: "user", rel: ".claude.json", entry: "a",
			in:   "{\"note\": \"\\\"mcpServers\\\": { \\\"a\\\": 1 }\", \"mcpServers\": {\"a\": {\"command\": \"x\", \"args\": [\"}\", \"{\"]}, \"b\": {\"command\": \"y\"}}}",
			want: "{\"note\": \"\\\"mcpServers\\\": { \\\"a\\\": 1 }\", \"mcpServers\": {\"b\": {\"command\": \"y\"}}}",
		},
		{
			name: "project .mcp.json", harness: "claude", scope: "project", rel: ".mcp.json", entry: "a",
			in:   "{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"},\n    \"b\": {\"command\": \"y\"}\n  }\n}\n",
			want: "{\n  \"mcpServers\": {\n    \"b\": {\"command\": \"y\"}\n  }\n}\n",
		},
		{
			name: "codex toml with comments and subtables", harness: "codex", scope: "user", rel: ".codex/config.toml", entry: "a",
			in:   "# top comment\nmodel = \"m1\"   # trailing\n\n[mcp_servers.a]\n# about a\ncommand = \"x\"\nargs = [\n  \"[mcp_servers.fake]\",\n  \"-y\",\n]\n\n[mcp_servers.a.env]\nFOO = \"${FOO}\"\n\n# about b\n[mcp_servers.b]\ncommand = \"y\"\nnote = \"\"\"\n[mcp_servers.a]\n\"\"\"\n\n[other]\nk = 1\n",
			want: "# top comment\nmodel = \"m1\"   # trailing\n\n# about b\n[mcp_servers.b]\ncommand = \"y\"\nnote = \"\"\"\n[mcp_servers.a]\n\"\"\"\n\n[other]\nk = 1\n",
		},
		{
			name: "codex toml last entry quoted key", harness: "codex", scope: "user", rel: ".codex/config.toml", entry: "my-srv",
			in:   "[mcp_servers.a]\ncommand = \"x\"\n\n[ mcp_servers . \"my-srv\" ]\ncommand = \"y\" # why\n",
			want: "[mcp_servers.a]\ncommand = \"x\"\n\n",
		},
		{
			name: "codex top-level inline table", harness: "codex", scope: "user", rel: ".codex/config.toml", entry: "alpha",
			in:   "model = \"m1\"\n# keep this comment\nmcp_servers.alpha = { command = \"x\", args = [\"-y\"] } # keep inline comment\n# keep this comment too\nmcp_servers.alpha2 = { command = \"keep\" }\n",
			want: "model = \"m1\"\n# keep this comment\n # keep inline comment\n# keep this comment too\nmcp_servers.alpha2 = { command = \"keep\" }\n",
		},
		{
			name: "codex inline table in servers table", harness: "codex", scope: "user", rel: ".codex/config.toml", entry: "alpha",
			in:   "[mcp_servers]\n# keep before\nalpha = { command = \"x\", args = [\"-y\"] }\n# keep after\nalpha2 = { command = \"keep\" }\n",
			want: "[mcp_servers]\n# keep before\n# keep after\nalpha2 = { command = \"keep\" }\n",
		},
		{
			name: "codex dotted keys with intervening comment and name prefix", harness: "codex", scope: "user", rel: ".codex/config.toml", entry: "alpha",
			in:   "mcp_servers.alpha.command = \"x\"\n# keep between dotted keys\nmcp_servers.alpha.args = [\"-y\"]\nmcp_servers.alpha2.command = \"keep-prefix\"\nmcp_servers.beta.command = \"keep-other\"\n",
			want: "# keep between dotted keys\nmcp_servers.alpha2.command = \"keep-prefix\"\nmcp_servers.beta.command = \"keep-other\"\n",
		},
		{
			name: "codex dotted keys inside servers table", harness: "codex", scope: "user", rel: ".codex/config.toml", entry: "alpha",
			in:   "[mcp_servers]\nalpha.command = \"x\"\n# keep between dotted keys\nalpha.args = [\"-y\"]\nalpha2.command = \"keep-prefix\"\n",
			want: "[mcp_servers]\n# keep between dotted keys\nalpha2.command = \"keep-prefix\"\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := strings.ReplaceAll(c.in, "ROOT", root)
			want := strings.ReplaceAll(c.want, "ROOT", root)
			got, err := removeVia(t, c.harness, c.scope, root, c.rel, in, c.entry)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

func TestRemoveNativeEntryRefusals(t *testing.T) {
	root := t.TempDir()
	if _, err := removeVia(t, "claude", "user", root, ".claude.json", `{"mcpServers": {"a": {"command": "x"}}}`, "zzz"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing entry: %v", err)
	}
	// Malformed files are never rewritten.
	if _, err := removeVia(t, "claude", "user", root, ".claude.json", `{"mcpServers": {"a": {"command": "x"}`, "a"); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	// A duplicate key is ambiguous.
	if _, err := removeVia(t, "claude", "user", root, ".claude.json", `{"mcpServers": {"a": {"command": "x"}, "a": {"command": "y"}}}`, "a"); err == nil {
		t.Fatal("duplicate key was accepted")
	}
	// A path that is not a known native source is refused.
	o := Options{Home: t.TempDir()}
	if _, err := RemoveNativeEntry(o, "claude", "user", "", "/etc/hostname", "a", []byte("{}")); err == nil {
		t.Fatal("unknown source accepted")
	}
}
