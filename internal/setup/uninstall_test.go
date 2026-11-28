package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/macsign"
)

func TestUninstallRemovesTheSigningIdentity(t *testing.T) {
	newFixture(t)
	t.Setenv("AGENT_NET_STATE_DIR", t.TempDir())
	dir := macsign.Dir(daemon.StateDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "keychain-password"), []byte("throwaway\n"), 0o600)
	out := uninstall(t)
	if !strings.Contains(out, "code signing identity ok (removed)") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("identity folder kept: %v", err)
	}
	if out := uninstall(t); strings.Contains(out, "code signing identity") {
		t.Errorf("a second uninstall reports an identity:\n%s", out)
	}
}
