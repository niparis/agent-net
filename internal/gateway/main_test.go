package gateway

import (
	"os"
	"sync"
	"testing"
)

// Migrating an empty database costs seconds under the race detector, and most
// tests only need an empty one. The first fresh database of a test process is
// migrated for real and its bytes are kept; every later fresh file starts as a
// copy of them and OpenStore still runs its checks and repairs on it. The
// migration itself stays covered by TestFreshAndUpgradedSchemasMatch and the
// migration tests, which open their files with openStore(path, nil) or from an
// older schema.
func TestMain(m *testing.M) {
	freshDatabaseSeed = seedFromTemplate
	os.Exit(m.Run())
}

var (
	templateOnce  sync.Once
	templateBytes []byte
	templateErr   error
)

// seedFromTemplate fills an empty database file with the migrated template.
// A file that already has content keeps it.
func seedFromTemplate(path string) error {
	if info, err := os.Stat(path); err != nil || info.Size() > 0 {
		return err
	}
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "agent-net-gw-template.")
		if err != nil {
			templateErr = err
			return
		}
		defer os.RemoveAll(dir)
		tpl := dir + "/gateway.db"
		st, err := openStore(tpl, nil)
		if err != nil {
			templateErr = err
			return
		}
		// Close checkpoints the WAL into the main file.
		if err := st.Close(); err != nil {
			templateErr = err
			return
		}
		templateBytes, templateErr = os.ReadFile(tpl)
	})
	if templateErr != nil {
		return templateErr
	}
	return os.WriteFile(path, templateBytes, 0o600)
}
