// Package claudeagents removes the Claude Code subagent files that older
// versions of agent-net wrote for the delegation policy (decision 0014, removed
// on 2026-11-21): ~/.claude/agents/agent-net-coder-<model>.md. Every file
// carries a marker, and only files with the prefix and the marker are removed.
package claudeagents

import (
	"os"
	"path/filepath"
	"strings"
)

// coderAgentPrefix starts the name of the subagent files agent-net wrote.
const coderAgentPrefix = "agent-net-coder-"

// Marker is the line that says agent-net owns the file.
const Marker = "<!-- managed by agent-net: written for your delegation policy, removed by `agent-net uninstall`; do not edit -->"

// Dir is the folder of the user's Claude Code subagents: $CLAUDE_CONFIG_DIR/agents,
// or ~/.claude/agents.
func Dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "agents")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		h = "."
	}
	return filepath.Join(h, ".claude", "agents")
}

// owned tells whether a file is one that agent-net wrote.
func owned(path string) bool {
	if !strings.HasPrefix(filepath.Base(path), coderAgentPrefix) || filepath.Ext(path) != ".md" {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), Marker)
}

// RemoveAll deletes every file agent-net wrote and returns their names. The
// folder stays.
func RemoveAll() ([]string, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gone []string
	for _, e := range entries {
		p := filepath.Join(Dir(), e.Name())
		if e.IsDir() || !owned(p) {
			continue
		}
		if err := os.Remove(p); err != nil {
			return gone, err
		}
		gone = append(gone, e.Name())
	}
	return gone, nil
}
