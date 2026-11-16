package inventory

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A skill of 6 MB is over the import limit (decision 0048) and under the
// archive limit (decision 0169). Only the archive limit takes it.
func TestArchiveLimitTakesSkillsOverTheImportLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte("x"), 3<<20), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := PackSkill(root); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("the import limit packed a 6 MB skill: %v", err)
	}
	packed, err := PackSkillLimit(root, ArchiveLimit)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err = UnpackSkillLimit(packed, out, ArchiveLimit); err != nil {
		t.Fatal(err)
	}
	want, _ := SkillHash(root)
	if got, _ := SkillHash(out); got != want {
		t.Fatalf("archive round trip hash %s, want %s", got, want)
	}
	if err = UnpackSkill(packed, t.TempDir()); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("the import limit unpacked a 6 MB bundle: %v", err)
	}
}

// The archive limit is a bound too: a skill past 64 MB of file data is refused
// before its bytes are read. A sparse file keeps the test small.
func TestArchiveLimitRefusesPastItsBound(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxArchiveBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = PackSkillLimit(root, ArchiveLimit); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("archive packed a skill past its bound: %v", err)
	}
}

// The archive takes more entries than an import, and the import still refuses
// the same bundle.
func TestArchiveLimitAllowsMoreEntriesThanImport(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for i := 0; i <= MaxBundleEntries; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("f%d", i), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := UnpackSkill(raw.Bytes(), t.TempDir()); err == nil {
		t.Fatal("the import limit took more entries than it allows")
	}
	if err := UnpackSkillLimit(raw.Bytes(), t.TempDir(), ArchiveLimit); err != nil {
		t.Fatalf("archive refused %d entries: %v", MaxBundleEntries+1, err)
	}
}
