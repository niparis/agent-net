package repoinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// graphProjectRoot rejects scratch roots while leaving paths on other machines
// alone. Registered project paths have already been checked by their daemon.
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
	parts := strings.Split(strings.Trim(root, "/"), "/")
	if len(parts) == 2 && (parts[0] == "home" || parts[0] == "Users") {
		return false
	}
	return true
}
