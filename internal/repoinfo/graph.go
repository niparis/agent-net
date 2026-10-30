package repoinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// GraphProjectRoot reports whether a local root can represent a graph component.
func GraphProjectRoot(root string) bool {
	if root == "" || !filepath.IsAbs(root) {
		return false
	}
	root = filepath.Clean(root)
	for _, dir := range []string{os.TempDir(), os.Getenv("TMPDIR"), "/tmp", "/var/folders", "/private/var/folders"} {
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if root == dir || strings.HasPrefix(root, dir+string(filepath.Separator)) {
			return false
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" && root == filepath.Clean(home) {
		return false
	}
	return true
}
