package repoinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentsHashChangesWithTheFile(t *testing.T) {
	dir := t.TempDir()
	if h := AgentsHash(dir); h != "" {
		t.Fatalf("hash without a file: %q", h)
	}
	if h := AgentsHash(""); h != "" {
		t.Fatalf("hash without a folder: %q", h)
	}
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := AgentsHash(dir)
	if first == "" || first != AgentsHash(dir) {
		t.Fatalf("hash is not stable: %q", first)
	}
	if err := os.WriteFile(path, []byte("# two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if AgentsHash(dir) == first {
		t.Fatal("hash did not change with the file")
	}
	// CLAUDE.md is the fallback when there is no AGENTS.md.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "CLAUDE.md"), []byte("# one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if AgentsHash(other) == "" {
		t.Fatal("no hash from CLAUDE.md")
	}
}
