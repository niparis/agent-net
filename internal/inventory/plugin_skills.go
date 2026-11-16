package inventory

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxPluginDepth bounds the walk below the plugin cache. The deepest layout
// seen is cache/<marketplace>/<plugin>/<version>/skills.
const maxPluginDepth = 6

// maxPluginDirs bounds how many folders one walk looks at.
const maxPluginDirs = 5000

// PluginSkillRoots lists the skills folders of the plugins that are installed
// in Claude Code. When ~/.claude/plugins/installed_plugins.json is readable,
// the install paths it lists are the answer, and nothing else is. Without it,
// only the installed-copy store (plugins/cache) is walked. A marketplace clone
// is never walked: it holds every plugin the marketplace offers, installed or
// not. Reads follow no link and run no plugin code.
func PluginSkillRoots(home string) []string {
	if roots, ok := installedPluginRoots(home); ok {
		return roots
	}
	var roots []string
	visited := 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxPluginDepth || visited >= maxPluginDirs {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			visited++
			path := filepath.Join(dir, name)
			if name == "skills" {
				roots = append(roots, path)
				continue
			}
			walk(path, depth+1)
		}
	}
	cache := filepath.Join(home, ".claude", "plugins", "cache")
	if safePath(home, cache) {
		walk(cache, 1)
	}
	return roots
}

// installedPluginRoots reads installed_plugins.json. ok is false when the file
// is missing, too large or not the expected shape. A plugin entry is an object
// or a list of objects (one per scope) with an installPath; the path must be
// inside the plugin store and free of links.
func installedPluginRoots(home string) (roots []string, ok bool) {
	store := filepath.Join(home, ".claude", "plugins")
	file := filepath.Join(store, "installed_plugins.json")
	if !safePath(home, file) {
		return nil, false
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, false
	}
	var list struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(data, &list) != nil || list.Plugins == nil {
		return nil, false
	}
	type install struct {
		InstallPath string `json:"installPath"`
	}
	seen := map[string]bool{}
	for _, raw := range list.Plugins {
		var installs []install
		if json.Unmarshal(raw, &installs) != nil {
			var one install
			if json.Unmarshal(raw, &one) != nil {
				continue
			}
			installs = []install{one}
		}
		for _, in := range installs {
			path := filepath.Clean(in.InstallPath)
			rel, err := filepath.Rel(store, path)
			if in.InstallPath == "" || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || !safePath(home, path) {
				continue
			}
			skills := filepath.Join(path, "skills")
			if info, err := os.Lstat(skills); err != nil || !info.IsDir() || seen[skills] {
				continue
			}
			seen[skills] = true
			roots = append(roots, skills)
		}
	}
	sort.Strings(roots)
	return roots, true
}

// PluginName names the plugin a path inside Claude Code's plugin store
// belongs to, or "" when the path is not in one. The store keeps installed
// copies as cache/<marketplace>/<plugin>/<version>/... and marketplace clones
// as marketplaces/<marketplace>/[plugins/<plugin>/]...; a skill kept directly
// in a marketplace belongs to the marketplace.
func PluginName(path string) string {
	const marker = "/.claude/plugins/"
	p := filepath.ToSlash(path)
	i := strings.Index(p, marker)
	if i < 0 {
		return ""
	}
	parts := strings.Split(p[i+len(marker):], "/")
	switch parts[0] {
	case "cache":
		if len(parts) > 3 {
			return parts[2]
		}
	case "marketplaces":
		for j := 2; j+1 < len(parts); j++ {
			if parts[j] == "plugins" {
				return parts[j+1]
			}
		}
		if len(parts) > 2 {
			return parts[1]
		}
	}
	return ""
}
