package agentfiles

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"path/filepath"
	"sort"

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

const claudeWorkersPluginManifest = ".claude-plugin/plugin.json"

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
	files, err := claudeWorkersFiles(agentnetworkers.Files())
	if err != nil {
		return nil
	}
	return files
}

func claudeWorkersFiles(pluginFiles map[string][]byte) (map[string][]byte, error) {
	manifest, err := versionedClaudeWorkersManifest(pluginFiles)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		filepath.ToSlash(filepath.Join(".claude-plugin", "marketplace.json")): []byte(claudeMarketplaceJSON),
	}
	for name, data := range pluginFiles {
		if name == claudeWorkersPluginManifest {
			data = manifest
		}
		files[filepath.ToSlash(filepath.Join(ClaudeWorkersPlugin, filepath.FromSlash(name)))] = data
	}
	return files, nil
}

func versionedClaudeWorkersManifest(pluginFiles map[string][]byte) ([]byte, error) {
	manifest, ok := pluginFiles[claudeWorkersPluginManifest]
	if !ok {
		return nil, fmt.Errorf("%s is missing", claudeWorkersPluginManifest)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &fields); err != nil {
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}
	var baseVersion string
	if err := json.Unmarshal(fields["version"], &baseVersion); err != nil || baseVersion == "" {
		if err == nil {
			err = fmt.Errorf("version is empty")
		}
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}

	h := sha256.New()
	hashClaudeWorkersFiles(h, pluginFiles)
	version, err := json.Marshal(baseVersion + "+" + hex.EncodeToString(h.Sum(nil)[:8]))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}
	fields["version"] = version
	b, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", claudeWorkersPluginManifest, err)
	}
	return append(b, '\n'), nil
}

func hashClaudeWorkersFiles(h hash.Hash, pluginFiles map[string][]byte) {
	names := make([]string, 0, len(pluginFiles))
	for name := range pluginFiles {
		if name != claudeWorkersPluginManifest {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		writeHashField(h, []byte(name))
		writeHashField(h, pluginFiles[name])
	}
}

func writeHashField(h hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

// WriteClaudeWorkers writes the marketplace and the mod files, creating the
// folder when it is not there. changed says whether any file was written.
func WriteClaudeWorkers() (changed bool, err error) {
	files, err := claudeWorkersFiles(agentnetworkers.Files())
	if err != nil {
		return false, err
	}
	for name, data := range files {
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
