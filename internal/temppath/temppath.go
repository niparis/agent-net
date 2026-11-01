// Package temppath tells whether a folder is under a system temporary
// folder. A session that runs there belongs to no project: it is a test run
// or a scratch session, not work on a repository.
package temppath

import (
	"os"
	"path/filepath"
	"strings"
)

// Default returns the system temporary folders: os.TempDir() (which follows
// TMPDIR), TMPDIR itself, the usual Linux and macOS folders, and the macOS
// per-user folders under /var/folders.
func Default() []string {
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		p = filepath.Clean(p)
		if p == "." || p == string(filepath.Separator) {
			return
		}
		for _, q := range out {
			if q == p {
				return
			}
		}
		out = append(out, p)
	}
	add(os.TempDir())
	add(os.Getenv("TMPDIR"))
	add("/tmp")
	add("/private/tmp")
	for _, pattern := range []string{"/var/folders/*/T", "/private/var/folders/*/T"} {
		if matches, err := filepath.Glob(pattern); err == nil {
			for _, m := range matches {
				add(m)
			}
		}
	}
	return out
}

// Under reports whether path is one of roots or below one of them. It is
// false for an empty path.
func Under(path string, roots []string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || root == "." {
			continue
		}
		if path == root {
			return true
		}
		if strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
