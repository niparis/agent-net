package executable

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChanged(t *testing.T) {
	for _, mode := range []string{"inode", "size", "mtime"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent-net")
			if err := os.WriteFile(path, []byte("abc"), 0o755); err != nil {
				t.Fatal(err)
			}
			old := time.Unix(1700000000, 0)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			changed := Changed(path)
			if changed() {
				t.Fatal("unchanged file reported as changed")
			}
			switch mode {
			case "inode":
				next := path + ".new"
				if err := os.WriteFile(next, []byte("abc"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(next, old, old); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(next, path); err != nil {
					t.Fatal(err)
				}
			case "size":
				if err := os.WriteFile(path, []byte("abcd"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			case "mtime":
				if err := os.Chtimes(path, old.Add(time.Second), old.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if !changed() {
				t.Fatal("change not detected")
			}
		})
	}
}
