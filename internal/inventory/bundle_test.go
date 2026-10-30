package inventory

import (
	"archive/tar"
	"bytes"
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
	b, err := PackSkill(root)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err = UnpackSkill(b, out); err != nil {
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
	if err = UnpackSkill(raw.Bytes(), t.TempDir()); err == nil {
		t.Fatal("accepted traversal")
	}
}
func TestBundleRejectsEscapingSymlinkAndLimits(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("../escape", filepath.Join(root, "bad")); err != nil {
		t.Fatal(err)
	}
	if _, err := PackSkill(root); err == nil {
		t.Fatal("accepted escaping symlink")
	}
	if err := UnpackSkill(bytes.Repeat([]byte("x"), MaxBundleBytes+1), t.TempDir()); err == nil {
		t.Fatal("accepted oversized bundle")
	}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for i := 0; i < maxFiles+1; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: strings.Repeat("x", 1) + fmt.Sprint(i), Mode: 0600, Size: 0}); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	if err := UnpackSkill(raw.Bytes(), t.TempDir()); err == nil {
		t.Fatal("accepted too many entries")
	}
}
