package agentfiles

import (
	"fmt"
	"path/filepath"

	agentnetworkers "github.com/niparis/agent-net/plugins/claude/agent-net-workers"
)

// The Claude Code "agent-net workers" plugin: a band above the prompt with the
// workers the session started, a status line entry, and a line when a worker
// ends. Claude Code installs plugins from a marketplace, so setup writes a
// small local marketplace and the mod beside it, then registers both with the
// claude CLI. The daemon refreshes the same folder on every start, so the
// mod always matches the binary, also after an upgrade that did not run setup.

// The marketplace and plugin names Claude Code knows.
const ClaudeWorkersMarketplace = "agent-net"
const ClaudeWorkersPlugin = "agent-net-workers"

// ClaudeWorkersDir is the local marketplace folder. It sits beside the gateway
// binaries in ~/.local/share/agent-net.
func ClaudeWorkersDir() string {
	return filepath.Join(home(), ".local", "share", "agent-net", "claude-plugins")
}

// ClaudeWorkersPluginDir is the mod inside the marketplace.
func ClaudeWorkersPluginDir() string {
	return filepath.Join(ClaudeWorkersDir(), ClaudeWorkersPlugin)
}

// claudeMarketplaceJSON lists the one plugin in the local marketplace.
const claudeMarketplaceJSON = `{
  "name": "agent-net",
  "owner": {
    "name": "agent-net"
  },
  "description": "Plugins that agent-net setup installs for Claude Code on this machine.",
  "plugins": [
    {
      "name": "agent-net-workers",
      "source": "./agent-net-workers"
    }
  ]
}
`

// ClaudeWorkersFiles lists every file the marketplace and the mod hold, keyed
// by its path below ClaudeWorkersDir.
func ClaudeWorkersFiles() map[string][]byte {
	files := map[string][]byte{
		filepath.ToSlash(filepath.Join(".claude-plugin", "marketplace.json")): []byte(claudeMarketplaceJSON),
	}
	for name, data := range agentnetworkers.Files() {
		files[filepath.ToSlash(filepath.Join(ClaudeWorkersPlugin, filepath.FromSlash(name)))] = data
	}
	return files
}

// WriteClaudeWorkers writes the marketplace and the mod files, creating the
// folder when it is not there. changed says whether any file was written.
func WriteClaudeWorkers() (changed bool, err error) {
	for name, data := range ClaudeWorkersFiles() {
		c, _, err := WriteIfChanged(filepath.Join(ClaudeWorkersDir(), filepath.FromSlash(name)), data, 0o644)
		if err != nil {
			return changed, fmt.Errorf("%s: %w", name, err)
		}
		changed = changed || c
	}
	return changed, nil
}

// SyncClaudeWorkers refreshes the marketplace only when setup already wrote
// it. A machine where setup was never asked for the mod gets no folder.
func SyncClaudeWorkers() Result {
	r := Result{Name: "claude workers mod", Path: ClaudeWorkersDir()}
	if !exists(r.Path) {
		r.Text = "skipped (" + DisplayPath(r.Path) + " not found)"
		return r
	}
	changed, err := WriteClaudeWorkers()
	r.Changed, r.Err = changed, err
	if err != nil {
		return r
	}
	r.Text = fileResult(changed, true)
	return r
}
