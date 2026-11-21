package inventory

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleRoundTripAndRejectTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := PackSkillLimit(root, ArchiveLimit)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err = UnpackSkillLimit(b, out, ArchiveLimit); err != nil {
		t.Fatal(err)
	}
	a, _ := SkillHash(root)
	c, _ := SkillHash(out)
	if a != c {
		t.Fatalf("hash %s != %s", a, c)
	}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0600, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	if err = UnpackSkillLimit(raw.Bytes(), t.TempDir(), ArchiveLimit); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestBundleRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("../escape", filepath.Join(root, "bad")); err != nil {
		t.Fatal(err)
	}
	if _, err := PackSkillLimit(root, ArchiveLimit); err == nil {
		t.Fatal("accepted escaping symlink")
	}
}

func TestUnpackRejectsEscapingSymlink(t *testing.T) {
	var raw bytes.Buffer
	w := tar.NewWriter(&raw)
	if err := w.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "../outside"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := UnpackSkillLimit(raw.Bytes(), t.TempDir(), ArchiveLimit); err == nil {
		t.Fatal("accepted escaping archive symlink")
	}
}

// A bundle past the byte limit is refused when packed and when unpacked.
func TestBundleLimitBoundsBytes(t *testing.T) {
	small := BundleLimit{Bytes: 1 << 20, Entries: 100}
	root := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte("x"), 1<<20), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := PackSkillLimit(root, small); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("a 2 MB skill was packed under a 1 MB limit: %v", err)
	}
	packed, err := PackSkillLimit(root, ArchiveLimit)
	if err != nil {
		t.Fatal(err)
	}
	if err = UnpackSkillLimit(packed, t.TempDir(), small); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("a 2 MB bundle was unpacked under a 1 MB limit: %v", err)
	}
	out := t.TempDir()
	if err = UnpackSkillLimit(packed, out, ArchiveLimit); err != nil {
		t.Fatal(err)
	}
	want, _ := SkillHash(root)
	if got, _ := SkillHash(out); got != want {
		t.Fatalf("archive round trip hash %s, want %s", got, want)
	}
}

// A bundle with more entries than the limit is refused, and a larger limit
// takes the same bundle.
func TestBundleLimitBoundsEntries(t *testing.T) {
	small := BundleLimit{Bytes: 1 << 20, Entries: 3}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for i := 0; i < 4; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("f%d", i), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := UnpackSkillLimit(raw.Bytes(), t.TempDir(), small); err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("entry limit not enforced: %v", err)
	}
	if err := UnpackSkillLimit(raw.Bytes(), t.TempDir(), ArchiveLimit); err != nil {
		t.Fatalf("archive refused 4 entries: %v", err)
	}
}

// The archive limit is a bound too: a skill past 64 MB of file data is refused
// before its bytes are read. A sparse file keeps the test small.
func TestArchiveLimitRefusesPastItsBound(t *testing.T) {
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "big.bin"))
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
