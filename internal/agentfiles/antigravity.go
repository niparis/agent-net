package agentfiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/guidance"
)

func AntigravityPluginDir() string {
	return filepath.Join(home(), ".gemini", "config", "plugins", "agent-net")
}

func antigravityFiles(exe string) map[string][]byte {
	marshal := func(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
	quote := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "'"
	hooks := map[string]any{}
	for _, event := range []string{"SessionStart", "PreInvocation", "Stop"} {
		hooks[event] = []any{map[string]any{"type": "command", "command": quote + " hook antigravity " + event, "timeout": 5}}
	}
	return map[string][]byte{
		"plugin.json":     marshal(map[string]any{"name": "agent-net", "description": "agent-net guidance, tools and hooks"}),
		"mcp_config.json": marshal(map[string]any{"mcpServers": map[string]any{"agent-net": map[string]any{"command": exe, "args": []string{"mcp", "--antigravity"}, "env": map[string]string{}}}}),
		"hooks.json":      marshal(map[string]any{"agent-net": hooks}),
		"rules/AGENTS.md": []byte(guidance.Block(guidance.Other)),
	}
}

// SyncAntigravity writes the plugin only when Antigravity's home exists.
func SyncAntigravity(exe string) Result {
	r := Result{Name: "antigravity plugin", Path: AntigravityPluginDir()}
	if !exists(filepath.Join(home(), ".gemini")) {
		r.Text = "skipped (~/.gemini not found)"
		return r
	}
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			r.Err = err
			return r
		}
	}
	// Do not follow symlinks anywhere below the harness home.
	for _, p := range []string{filepath.Join(home(), ".gemini"), filepath.Join(home(), ".gemini", "config"), filepath.Dir(r.Path), r.Path, filepath.Join(r.Path, "rules")} {
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			r.Symlink = true
			r.Text = "skipped (symlink, left as is)"
			return r
		} else if err != nil && !os.IsNotExist(err) {
			r.Err = err
			return r
		}
	}
	for name := range antigravityFiles(exe) {
		p := filepath.Join(r.Path, name)
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			r.Symlink = true
			r.Text = "skipped (symlink, left as is)"
			return r
		} else if err != nil && !os.IsNotExist(err) {
			r.Err = err
			return r
		}
	}
	manifest := filepath.Join(r.Path, "plugin.json")
	if b, err := os.ReadFile(manifest); err == nil {
		if !ownedAntigravityManifest(b) {
			r.Text = "skipped (foreign plugin, left as is)"
			return r
		}
	} else if os.IsNotExist(err) {
		if entries, e := os.ReadDir(r.Path); e == nil && len(entries) != 0 {
			r.Text = "skipped (foreign plugin, left as is)"
			return r
		} else if e != nil && !os.IsNotExist(e) {
			r.Err = e
			return r
		}
	} else {
		r.Err = err
		return r
	}
	for name, data := range antigravityFiles(exe) {
		p := filepath.Join(r.Path, name)
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			r.Symlink = true
			r.Text = "skipped (symlink, left as is)"
			return r
		} else if err != nil && !os.IsNotExist(err) {
			r.Err = err
			return r
		}
		changed, _, err := WriteIfChanged(p, data, 0644)
		if err != nil {
			r.Err = fmt.Errorf("%s: %w", name, err)
			return r
		}
		r.Changed = r.Changed || changed
	}
	r.Text = fileResult(r.Changed, false)
	return r
}

// RemoveAntigravity removes only the files belonging to this plugin.
func RemoveAntigravity() error {
	dir := AntigravityPluginDir()
	for _, p := range []string{filepath.Join(home(), ".gemini"), filepath.Join(home(), ".gemini", "config"), filepath.Dir(dir), dir, filepath.Join(dir, "rules")} {
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil
		}
	}
	// The manifest identifies the directory as ours. Leave foreign plugins alone.
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownedAntigravityManifest(b) {
		return nil
	}
	var config struct {
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	configBytes, err := os.ReadFile(filepath.Join(dir, "mcp_config.json"))
	if err != nil || json.Unmarshal(configBytes, &config) != nil {
		return nil
	}
	exe := config.Servers["agent-net"].Command
	if exe == "" {
		return nil
	}
	files := antigravityFiles(exe)

	for _, name := range []string{"rules/AGENTS.md", "mcp_config.json", "hooks.json", "plugin.json"} {
		p := filepath.Join(dir, name)
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, files[name]) {
			continue
		}
		if err = os.Remove(p); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(dir, "rules"))
	_ = os.Remove(dir)
	return nil
}

func ownedAntigravityManifest(b []byte) bool {
	var m struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	return json.Unmarshal(b, &m) == nil && m.Name == "agent-net" && m.Description == "agent-net guidance, tools and hooks"
}
