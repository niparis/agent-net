// Package agentfiles writes the files that agent harnesses load: the Pi
// extension, the OpenCode plugin and the guidance blocks. `agent-net setup`
// and the daemon (at start) both call it, so a binary that was copied or
// upgraded without running setup still brings these files up to date.
package agentfiles

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/plugins/opencode"
	"github.com/niparis/agent-net/plugins/pi"
)

// Result is the outcome for one managed file.
type Result struct {
	Name    string // "pi extension", "claude guidance", ...
	Path    string
	Text    string // the result text setup prints, like "updated" or "skipped (symlink, left as is)"
	Changed bool   // the file was written
	Symlink bool   // the file is a symlink to a foreign file and was left alone
	Err     error
}

// The first line of every version of the plugin files agent-net writes. A
// symlink whose target starts with one of these holds an agent-net copy.
const (
	piPluginMarker       = "// agent-net extension for Pi"
	opencodePluginMarker = "// agent-net plugin for OpenCode"
)

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// PiExtensionPath is where the Pi extension lives.
func PiExtensionPath() string {
	return filepath.Join(home(), ".pi", "agent", "extensions", "agent-net.ts")
}

// OpencodeDir is OpenCode's global config directory. OpenCode v2 loads every
// file in its plugins/ subdirectory and reads AGENTS.md in it as global
// instructions.
func OpencodeDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode")
	}
	return filepath.Join(home(), ".config", "opencode")
}

// OpencodePluginPath is where the OpenCode plugin lives.
func OpencodePluginPath() string {
	return filepath.Join(OpencodeDir(), "plugins", "agent-net.ts")
}

// CodexHome is Codex's home folder: $CODEX_HOME, else ~/.codex. It holds the
// config.toml that setup registers the MCP server in, the AGENTS.md that Codex
// reads as global instructions, and the control socket of the shared app server.
func CodexHome() string {
	if x := os.Getenv("CODEX_HOME"); x != "" {
		return x
	}
	return filepath.Join(home(), ".codex")
}

// CodexConfigPath is Codex's config.toml.
func CodexConfigPath() string { return filepath.Join(CodexHome(), "config.toml") }

// GuidanceFile is a file that holds an agent-net guidance block.
type GuidanceFile struct {
	Name    string
	Dir     string // must exist for the file to be touched
	Path    string
	Variant string
}

// GuidanceFiles lists the guidance files setup manages.
func GuidanceFiles() []GuidanceFile {
	h := home()
	return []GuidanceFile{
		{"claude guidance", filepath.Join(h, ".claude"), filepath.Join(h, ".claude", "CLAUDE.md"), guidance.Claude},
		{"agent guidance", filepath.Join(h, ".pi"), filepath.Join(h, ".pi", "agent", "AGENTS.md"), guidance.Other},
		{"opencode guidance", OpencodeDir(), filepath.Join(OpencodeDir(), "AGENTS.md"), guidance.Other},
		{"codex guidance", CodexHome(), filepath.Join(CodexHome(), "AGENTS.md"), guidance.Other},
	}
}

// Sync brings the Pi extension, the OpenCode plugin and the guidance blocks in
// line with this binary. It only touches what setup would manage: the harness
// folder must exist. A plugin symlink to a foreign file is left alone; a
// symlink to an older agent-net plugin is replaced with a real file. Guidance
// follows symlinks and changes only the part between the markers.
func Sync() []Result {
	rs := []Result{SyncPi(), SyncOpencode(), SyncClaudeWorkers()}
	return append(rs, SyncGuidance()...)
}

// SyncAndLog runs Sync and logs one line for each file it changed, failed on
// or left alone because it is a symlink to a foreign file. Files that are up
// to date, and harnesses that are not installed, log nothing.
func SyncAndLog(logf func(format string, args ...any)) {
	for _, r := range Sync() {
		switch {
		case r.Err != nil:
			logf("agent files: %s %s: %v", r.Name, DisplayPath(r.Path), r.Err)
		case r.Symlink:
			logf("agent files: %s left alone (symlink): %s", r.Name, DisplayPath(r.Path))
		case r.Changed && r.Text == "updated":
			logf("agent files: %s updated: %s", r.Name, DisplayPath(r.Path))
		case r.Changed && strings.HasPrefix(r.Text, "replaced "):
			logf("agent files: %s %s: %s", r.Name, r.Text, DisplayPath(r.Path))
		case r.Changed:
			logf("agent files: %s created: %s", r.Name, DisplayPath(r.Path))
		}
	}
}

// SyncPi installs the Pi extension.
func SyncPi() Result {
	r := Result{Name: "pi extension", Path: PiExtensionPath()}
	if !exists(filepath.Join(home(), ".pi")) {
		r.Text = "skipped (~/.pi not found)"
		return r
	}
	return syncPlugin(r, pi.Extension, piPluginMarker)
}

// SyncOpencode installs the OpenCode plugin.
func SyncOpencode() Result {
	r := Result{Name: "opencode plugin", Path: OpencodePluginPath()}
	if !exists(OpencodeDir()) {
		r.Text = "skipped (" + DisplayPath(OpencodeDir()) + " not found)"
		return r
	}
	return syncPlugin(r, opencode.Plugin, opencodePluginMarker)
}

func syncPlugin(r Result, data []byte, marker string) Result {
	if st, err := os.Lstat(r.Path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(r.Path)
		if err != nil {
			r.Err = err
			return r
		}
		linked, err := os.ReadFile(r.Path)
		if err != nil || bytes.Equal(linked, data) || !hasPluginMarker(linked, marker) {
			r.Text = "skipped (symlink, left as is)"
			r.Symlink = true
			return r
		}
		if err := replaceSymlink(r.Path, data); err != nil {
			r.Err = err
			return r
		}
		r.Changed = true
		r.Text = "replaced an old symlink to " + target + " with the current file"
		return r
	}
	changed, existed, err := WriteIfChanged(r.Path, data, 0o644)
	r.Changed, r.Err = changed, err
	r.Text = fileResult(changed, existed)
	return r
}

// hasPluginMarker reports whether data starts with the header agent-net writes
// for the harness named by marker. The header line is stable across versions,
// so a missing trust note or an old tool list still starts with it.
func hasPluginMarker(data []byte, marker string) bool {
	line := data
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return bytes.HasPrefix(line, []byte(marker))
}

// isAgentNetPlugin reports whether data is one of the plugin files agent-net
// writes, for either harness.
func isAgentNetPlugin(data []byte) bool {
	return hasPluginMarker(data, piPluginMarker) || hasPluginMarker(data, opencodePluginMarker)
}

// SymlinkTargetIsAgentNet reports whether path is a symlink to a file that
// holds an agent-net plugin header. A broken link or a foreign file is false.
func SymlinkTargetIsAgentNet(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && isAgentNetPlugin(data)
}

// replaceSymlink replaces the symlink at path with a real file holding data.
// The rename changes the link itself, never the file it points at.
func replaceSymlink(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// SyncGuidance writes the guidance blocks.
func SyncGuidance() []Result {
	files := GuidanceFiles()
	keys := make([]string, len(files))
	variants := make(map[string]map[string]bool)
	for i, f := range files {
		if !exists(f.Dir) {
			continue
		}
		key, err := filepath.EvalSymlinks(f.Path)
		if err != nil {
			key = f.Path
		}
		keys[i] = key
		if variants[key] == nil {
			variants[key] = make(map[string]bool)
		}
		variants[key][f.Variant] = true
	}
	var rs []Result
	seen := make(map[string]Result)
	for i, f := range files {
		key := keys[i]
		if previous, ok := seen[key]; key != "" && ok {
			r := Result{Name: f.Name, Path: f.Path, Text: "ok (shared guidance)", Err: previous.Err}
			rs = append(rs, r)
			continue
		}
		if len(variants[key]) > 1 {
			f.Variant = guidance.Combined
		}
		r := syncGuidance(f)
		rs = append(rs, r)
		if key != "" {
			seen[key] = r
		}
	}
	return rs
}

func syncGuidance(f GuidanceFile) Result {
	r := Result{Name: f.Name, Path: f.Path}
	if !exists(f.Dir) {
		r.Text = "skipped (" + DisplayPath(f.Dir) + " not found)"
		return r
	}
	old, err := os.ReadFile(f.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		r.Err = err
		return r
	}
	out, changed := guidance.Apply(string(old), f.Variant)
	if !changed {
		r.Text = "ok (unchanged)"
		return r
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		r.Err = err
		return r
	}
	// Write through a symlink if the file is one, keep its mode.
	mode := os.FileMode(0o644)
	if st, err := os.Stat(f.Path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(f.Path, []byte(out), mode); err != nil {
		r.Err = err
		return r
	}
	r.Changed = true
	if len(old) == 0 {
		r.Text = "ok"
	} else {
		r.Text = "updated"
	}
	return r
}

func fileResult(changed, existed bool) string {
	switch {
	case !changed:
		return "ok (unchanged)"
	case existed:
		return "updated"
	}
	return "ok"
}

// DisplayPath shortens a path under the home dir to ~/...
func DisplayPath(p string) string {
	if h := home(); strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// WriteIfChanged writes data to path (creating parent dirs) unless it
// already holds exactly that.
func WriteIfChanged(path string, data []byte, mode os.FileMode) (changed, existed bool, err error) {
	old, rerr := os.ReadFile(path)
	if rerr == nil {
		existed = true
		if bytes.Equal(old, data) {
			return false, true, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, existed, err
	}
	return true, existed, os.WriteFile(path, data, mode)
}
