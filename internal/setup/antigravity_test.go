package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravitySetupUninstall(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	if err := os.MkdirAll(filepath.Join(f.home, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "antigravity plugin ok") {
		t.Fatalf("setup: %s", out)
	}
	dir := filepath.Join(f.home, ".gemini", "config", "plugins", "agent-net")
	if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Uninstall(Options{Out: &buf, Exe: "/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("plugin remains: %v", err)
	}
}
