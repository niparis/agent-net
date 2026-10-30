package agentnetworkers

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The embedded files are the ones in this folder, so setup can never install
// an old copy after the mod changes.
func TestEmbeddedFilesMatchTheFolder(t *testing.T) {
	for name, data := range Files() {
		want, err := os.ReadFile(filepath.Join(".", filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(data, want) {
			t.Errorf("%s is not the file in the plugin folder", name)
		}
	}
	if len(Files()) != 4 {
		t.Errorf("embedded %d files, want the 4 plugin files", len(Files()))
	}
	if _, ok := Files()["hooks/register.test.tsx"]; ok {
		t.Error("the test file is embedded")
	}
}

// A rename or a new plugin file that is not embedded would leave setup with an
// incomplete plugin. The folder holds exactly the embedded files and the test.
func TestThePluginFolderHoldsOnlyEmbeddedFilesAndTheTest(t *testing.T) {
	var found []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		name := filepath.ToSlash(path)
		if name == "embed.go" || name == "embed_test.go" || name == "hooks/register.test.tsx" {
			return nil
		}
		found = append(found, name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != len(Files()) {
		t.Fatalf("plugin folder files: %v", found)
	}
	for _, name := range found {
		if _, ok := Files()[name]; !ok {
			t.Errorf("%s is in the folder but not embedded", name)
		}
	}
}
