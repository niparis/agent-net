//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheOpenProcessDetection(t *testing.T) {
	proc := t.TempDir()
	dir := t.TempDir()
	fd := filepath.Join(proc, "123", "fd")
	if err := os.MkdirAll(fd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "held"), filepath.Join(fd, "1")); err != nil {
		t.Fatal(err)
	}
	used, err := processHasOpenPathIn(proc, dir)
	if err != nil || !used {
		t.Fatalf("open file: used=%v err=%v", used, err)
	}
	if err := os.Remove(filepath.Join(fd, "1")); err != nil {
		t.Fatal(err)
	}
	used, err = processHasOpenPathIn(proc, dir)
	if err != nil || used {
		t.Fatalf("closed file: used=%v err=%v", used, err)
	}
}

func TestCacheProcessWorkingDirectoryInUse(t *testing.T) {
	proc := t.TempDir()
	dir := t.TempDir()
	pid := filepath.Join(proc, "123")
	if err := os.MkdirAll(filepath.Join(pid, "fd"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(pid, "cwd")); err != nil {
		t.Fatal(err)
	}
	used, err := processHasOpenPathIn(proc, dir)
	if err != nil || !used {
		t.Fatalf("cwd: used=%v error=%v", used, err)
	}
}

func TestCacheProcessPathNamedDeleted(t *testing.T) {
	proc := t.TempDir()
	parent := t.TempDir()
	dir := filepath.Join(parent, "cache (deleted)")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fd := filepath.Join(proc, "123", "fd")
	if err := os.MkdirAll(fd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "file"), filepath.Join(fd, "1")); err != nil {
		t.Fatal(err)
	}
	used, err := processHasOpenPathIn(proc, dir)
	if err != nil || !used {
		t.Fatalf("open file: used=%v error=%v", used, err)
	}
}
