package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ctoOwnerConfig copies settings but never the owner's agents or MCP servers:
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
	for _, k := range []string{"agent", "agents", "mcp", "permission", "instructions", "server", "autoupdate"} {
		delete(cfg, k)
	}
	if plugins, ok := cfg["plugin"].([]any); ok {
		filtered := make([]any, 0, len(plugins))
		for _, p := range plugins {
			s, ok := p.(string)
			if !ok || !isAgentNetPlugin(s) {
				filtered = append(filtered, p)
			}
		}
		cfg["plugin"] = filtered
	}
	for _, name := range []string{"plugin", "plugins"} {
		target := filepath.Join(private, "opencode", name)
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(filepath.Join(owner, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || isAgentNetPlugin(e.Name()) {
				continue
			}
			if err := os.MkdirAll(target, 0700); err != nil {
				return nil, err
			}
			if err := os.Symlink(filepath.Join(owner, name, e.Name()), filepath.Join(target, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	return cfg, nil
}

func isAgentNetPlugin(s string) bool { return strings.Contains(strings.ToLower(s), "agent-net") }

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
