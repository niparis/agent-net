package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodePrivateAuth(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\ncase \"$2\" in\nexport) printf '[{\"secret\":\"test\"}]' ;;\nimport) test \"$XDG_DATA_HOME\" = \"$EXPECTED_DATA\" || exit 1; umask 077; mkdir -p \"$XDG_DATA_HOME/opencode\"; cat > \"$XDG_DATA_HOME/opencode/imported\" ;;\n*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "worker", "data")
	t.Setenv("EXPECTED_DATA", data)
	if err := copyOpenCodeAuth(t.Context(), bin, data, filepath.Join(dir, "worker", "state")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(data, "opencode", "imported"))
	if err != nil || string(b) != `[{"secret":"test"}]` {
		t.Fatalf("import: %q %v", b, err)
	}
}
