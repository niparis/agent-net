// Package executable detects replacement of a running executable.
package executable

import "os"

// Changed captures the file at path and reports whether its identity, size or
// modification time has since changed. A missing file is not an upgrade.
func Changed(path string) func() bool {
	original, err := os.Stat(path)
	if err != nil {
		return func() bool { return false }
	}
	return func() bool {
		current, err := os.Stat(path)
		return err == nil && (!os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()))
	}
}
