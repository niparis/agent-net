package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonTestsUseFakeQuotaAndConfig(t *testing.T) {
	root := os.Getenv(envFakePids)
	path, ok := FindQuotaAXI("")
	if !ok || !strings.HasPrefix(path, root+string(filepath.Separator)) {
		t.Errorf("quota executable is not in the test directory: %q", path)
	}
	if dir := ConfigDir(); !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		t.Errorf("config directory is not in the test directory: %q", dir)
	}
}
