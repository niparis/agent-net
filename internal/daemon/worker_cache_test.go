package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
		if err := os.Chtimes(filepath.Join(p, "file"), past, past); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	freed, err := trimFolder(root, time.Now().Add(-2*time.Hour), func(path string) (bool, error) { return filepath.Base(path) == "open", nil })
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

func TestTrimTempInconclusiveProcessCheck(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "old")
	if err := os.Mkdir(entry, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(entry, "file")
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	for _, p := range []string{file, entry} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	freed, err := trimFolder(root, time.Now().Add(-2*time.Hour), func(string) (bool, error) { return false, errors.New("inconclusive") })
	if err != nil || freed != 3 {
		t.Fatalf("trim: freed=%d err=%v", freed, err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("old entry remains: %v", err)
	}
}

func TestTrimTempKeepsRecentDescendant(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "old")
	if err := os.Mkdir(entry, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(entry, "active")
	if err := os.WriteFile(file, []byte("fresh"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(entry, old, old); err != nil {
		t.Fatal(err)
	}
	freed, err := trimFolder(root, time.Now().Add(-2*time.Hour), func(string) (bool, error) { return false, nil })
	if err != nil || freed != 0 {
		t.Fatalf("trim: freed=%d err=%v", freed, err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("recent file removed: %v", err)
	}
}
