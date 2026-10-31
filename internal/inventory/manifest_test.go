package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillManifestContentsAndLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("noise"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "SKILL.md"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	manifest, err := skillManifestRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TooLarge || len(manifest.Files) != 1 || manifest.Files[0].Path != "SKILL.md" || manifest.Files[0].Size != 5 || manifest.Files[0].Fingerprint == "" || !manifest.Newest.Equal(stamp) {
		t.Fatalf("manifest: %+v", manifest)
	}
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err = skillManifestRoot(dir)
	if err != nil || !manifest.TooLarge || len(manifest.Files) != 0 {
		t.Fatalf("limit: %+v %v", manifest, err)
	}
}

func TestScanIncludesSkillManifest(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	report := Scan(Options{Home: home})
	for _, item := range report.Items {
		if item.Harness == "claude" && item.Name == "demo" {
			if item.Manifest == nil || len(item.Manifest.Files) != 1 {
				t.Fatalf("manifest: %+v", item.Manifest)
			}
			return
		}
	}
	t.Fatal("missing skill")
}
