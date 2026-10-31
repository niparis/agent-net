package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillHashDeterministicAndSensitive(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := SkillHash(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SkillHash(root)
	if err != nil || a != b {
		t.Fatalf("unstable hash: %q %q %v", a, b, err)
	}
	if err := os.Chmod(filepath.Join(root, "SKILL.md"), 0700); err != nil {
		t.Fatal(err)
	}
	b, err = SkillHash(root)
	if err != nil || a == b {
		t.Fatalf("mode ignored: %q %q %v", a, b, err)
	}
}

func TestSkillHashRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := SkillHash(root); err == nil {
		t.Fatal("accepted escaping link")
	}
}

func TestSkillHashInTreeLinkAndSizeLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("SKILL.md", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := SkillHash(root); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := SkillHash(root); err == nil {
		t.Fatal("accepted oversized skill")
	}
}

func TestContentFingerprintIgnoresNoiseAndMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	before, err := skillContentFingerprintRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "SKILL.md"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{".DS_Store", "Thumbs.db", ".SKILL.md.swp", "file.swo", "file~"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n), 0600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := skillContentFingerprintRoot(dir)
	if err != nil || before != after {
		t.Fatalf("content fingerprint changed: %s %s %v", before, after, err)
	}
}
