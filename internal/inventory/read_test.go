package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSkillTextRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"ok": "hello", "binary": "a\x00b", "controls": "a\x1bb", "unicode-controls": "a\u0085b", "large": strings.Repeat("x", 65537)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("ok", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSkillText(dir, "ok"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../ok", "/etc/passwd", "link", "binary", "controls", "unicode-controls", "large"} {
		if _, err := ReadSkillText(dir, name); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
}
