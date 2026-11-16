package inventory

import (
	"os"
	"path/filepath"
	"strings"
)

// maxPluginDepth bounds the walk below a plugin store. The deepest layout
// seen is cache/<marketplace>/<plugin>/<version>/skills.
const maxPluginDepth = 6

// maxPluginDirs bounds how many folders one walk looks at.
const maxPluginDirs = 5000

// PluginSkillRoots lists the folders named "skills" inside Claude Code's
// plugin store (~/.claude/plugins/cache and marketplaces). It reads folder
// names only: it follows no link, skips node_modules and dot folders, and does
// not go into a skills folder it found.
func PluginSkillRoots(home string) []string {
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
	for _, store := range []string{"cache", "marketplaces"} {
		dir := filepath.Join(home, ".claude", "plugins", store)
		if safePath(home, dir) {
			walk(dir, 1)
		}
	}
	return roots
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
