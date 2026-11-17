package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDocsFlagsEmDashLines(t *testing.T) {
	dir := t.TempDir()
	// The escape makes the file hold an em dash. The file itself stays plain.
	write(t, filepath.Join(dir, "guide.md"), "# Guide\n\nRun it \u2014 then stop.\nRun it - then stop.\n")
	write(t, filepath.Join(dir, "notes.txt"), "Run it \u2014 not a doc.\n")

	got, err := checkDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one finding, got %v", got)
	}
	f := got[0]
	if filepath.Base(f.file) != "guide.md" || f.line != 3 || f.col != 8 || f.rule != "emdash" {
		t.Fatalf("finding %+v, want guide.md line 3 column 8", f)
	}
}

func TestCheckDocsPassesPlainDocs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "plain.md"), "Run it - then stop.\n")
	got, err := checkDocs(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
