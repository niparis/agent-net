package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrimCacheOldFilesOnly(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	fresh := filepath.Join(root, "fresh")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("1234"), 0400); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	freed, err := trimGoCache(root, time.Now().Add(-2*time.Hour))
	if err != nil || freed != 4 {
		t.Fatalf("trim: %d %v", freed, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file remains: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file removed: %v", err)
	}
}

func TestTrimTempSkipsOpenEntries(t *testing.T) {
	root := t.TempDir()
	past := time.Now().Add(-3 * time.Hour)
	for _, name := range []string{"closed", "open"} {
		p := filepath.Join(root, name)
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "file"), []byte("1234"), 0400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	freed, err := trimGoTemp(root, time.Now().Add(-2*time.Hour), func(path string) (bool, error) { return filepath.Base(path) == "open", nil })
	if err != nil || freed != 4 {
		t.Fatalf("trim: %d %v", freed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "closed")); !os.IsNotExist(err) {
		t.Fatalf("closed entry remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "open")); err != nil {
		t.Fatalf("open entry removed: %v", err)
	}
}
