package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ctoOwnerConfig copies settings but never agents, plugins or MCP servers:
// those could expose tools outside the CTO's private MCP boundary.
func ctoOwnerConfig(private string) (map[string]any, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".config")
	}
	owner := filepath.Join(root, "opencode")
	for _, name := range []string{"plugin", "plugins"} {
		if err := os.RemoveAll(filepath.Join(private, "opencode", name)); err != nil {
			return nil, err
		}
	}
	cfg := make(map[string]any)
	paths := []string{filepath.Join(owner, "config.json"), filepath.Join(owner, "opencode.json"), filepath.Join(owner, "opencode.jsonc")}
	if path := os.Getenv("OPENCODE_CONFIG"); path != "" {
		paths = append(paths, path)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) && path != os.Getenv("OPENCODE_CONFIG") {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var part map[string]any
		if err := json.Unmarshal(cleanJSONC(data), &part); err != nil || part == nil {
			return nil, fmt.Errorf("invalid OpenCode config %s", path)
		}
		for k, v := range part {
			cfg[k] = v
		}
	}
	for _, k := range []string{"agent", "agents", "mcp", "permission", "permissions", "plugin", "plugins", "instructions", "server", "autoupdate"} {
		delete(cfg, k)
	}

	return cfg, nil
}

// cleanJSONC removes comments and trailing commas without changing strings.
func cleanJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString, escape := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(b) {
			if b[i+1] == '/' {
				i += 2
				for i < len(b) && b[i] != '\n' {
					i++
				}
				out = append(out, '\n')
				continue
			}
			if b[i+1] == '*' {
				i += 2
				for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
					i++
				}
				if i+1 >= len(b) {
					return b
				}
				i++
				out = append(out, ' ')
				continue
			}
		}
		out = append(out, c)
	}
	b = out
	out = make([]byte, 0, len(b))
	inString, escape = false, false
	for i, c := range b {
		if inString {
			out = append(out, c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
		}
		if c == ',' {
			j := i + 1
			for j < len(b) && strings.ContainsRune(" \t\r\n", rune(b[j])) {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}
