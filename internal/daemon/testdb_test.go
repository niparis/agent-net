package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
)

// Creating a database schema costs about a second with the race detector: the
// schema is many statements and each one is a transaction, so the disk syncs
// once per statement. A test that only needs an empty database does not need
// to pay that again, so the first one migrates a template and the others write
// its bytes. The migration itself stays covered by every test that calls
// openState or gateway.OpenStore with a directory of its own, and by the
// migration tests.

// migratedTemplate migrates an empty database once and keeps its bytes.
type migratedTemplate struct {
	once  sync.Once
	bytes []byte
	err   error
}

// read runs migrate on a temporary directory and returns the database file.
func (m *migratedTemplate) read(name string, migrate func(dir string) error) ([]byte, error) {
	m.once.Do(func() {
		dir, err := os.MkdirTemp("", "agent-net-dbtemplate.")
		if err != nil {
			m.err = err
			return
		}
		defer os.RemoveAll(dir)
		if err := migrate(dir); err != nil {
			m.err = err
			return
		}
		m.bytes, m.err = os.ReadFile(filepath.Join(dir, name))
	})
	return m.bytes, m.err
}

var stateTemplate = &migratedTemplate{}

// stateTemplateBytes is the daemon database that openState migrated once.
func stateTemplateBytes() ([]byte, error) {
	return stateTemplate.read("daemon.db", func(dir string) error {
		st, err := openState(dir)
		if err != nil {
			return err
		}
		return st.close()
	})
}

var gatewayTemplate = &migratedTemplate{}

// gatewayTemplateBytes is the gateway database that gateway.New migrated once.
func gatewayTemplateBytes() ([]byte, error) {
	return gatewayTemplate.read("gateway.db", func(dir string) error {
		gw, err := gateway.New(gateway.Options{DataDir: dir})
		if err != nil {
			return err
		}
		return gw.Close()
	})
}

// seedDatabase writes a migrated empty database into dir unless one is there.
func seedDatabase(t *testing.T, dir, name string, template func() ([]byte, error)) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return // a second daemon or gateway on the same directory keeps its data
	}
	raw, err := template()
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		t.Fatalf("the %s template is empty", name)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// warmTemplates migrates the templates before the first test runs. The
// migration is many synced statements, ten seconds or more with the race
// detector on a busy disk. A test that pays for it while other tests run makes
// them race their scaled deadlines against that disk work, so pay it once,
// before any of them starts. TestMain calls it.
func warmTemplates() error {
	if _, err := gatewayTemplateBytes(); err != nil {
		return err
	}
	_, err := stateTemplateBytes()
	return err
}

// seededStateDir is a new state directory holding an empty migrated database.
func seededStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seedDatabase(t, dir, "daemon.db", stateTemplateBytes)
	return dir
}
