// Package claudeagents writes and removes the Claude Code subagent files that
// agent-net owns (decision 0014). A file is one coder subagent for the
// delegation policy: ~/.claude/agents/agent-net-coder-<model>.md with a
// "model:" line. Every file carries a marker, and only files with the prefix
// and the marker are ever changed or removed.
package claudeagents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

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

// Content is the file for one coder subagent. name is the agent ID (from
// protocol.CoderAgentID) and model the value of "model:" (a Claude Code alias
// such as sonnet, or a full model ID). Both hold only name characters
// (DelegationPolicy.Clean), so nothing can break out of the frontmatter.
func Content(name, model string) string {
	return "---\n" +
		"name: " + name + "\n" +
		"description: " + protocol.CoderAgentDescription + "\n" +
		"model: " + model + "\n" +
		"---\n" +
		Marker + "\n\n" +
		protocol.CoderAgentPrompt + "\n"
}

// owned tells whether a file is one that agent-net wrote.
func owned(path string) bool {
	if !strings.HasPrefix(filepath.Base(path), protocol.CoderAgentPrefix) || filepath.Ext(path) != ".md" {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), Marker)
}

// Ensure writes the coder subagent for a model when it is missing or changed.
// A file with the same name that agent-net did not write is left alone and
// reported as an error. It returns true when it wrote the file.
func Ensure(name, model string) (bool, error) {
	if !strings.HasPrefix(name, protocol.CoderAgentPrefix) || strings.ContainsAny(name, `/\`) {
		return false, errors.New("not an agent-net coder name: " + name)
	}
	path := filepath.Join(Dir(), name+".md")
	want := Content(name, model)
	if b, err := os.ReadFile(path); err == nil {
		if string(b) == want {
			return false, nil
		}
		if !strings.Contains(string(b), Marker) {
			return false, errors.New(path + " exists and was not written by agent-net: left alone")
		}
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(Dir(), "."+name+".*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(want); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
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
