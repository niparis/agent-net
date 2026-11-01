package gateway

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Creating the store schema costs up to a second with the race detector: the
// migrations are many statements and each one is a transaction, so the disk
// syncs once per statement. A test that only needs an empty store does not
// need to pay that again, so the first one migrates a template and the others
// write its bytes. The migrations stay covered by every test that calls
// OpenStore with a path of its own, and by the migration tests.

var storeTemplate struct {
	once  sync.Once
	bytes []byte
	err   error
}

// storeTemplateBytes is the store database that OpenStore migrated once.
func storeTemplateBytes() ([]byte, error) {
	storeTemplate.once.Do(func() {
		dir, err := os.MkdirTemp("", "agent-net-store-template.")
		if err != nil {
			storeTemplate.err = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "gateway.db")
		s, err := OpenStore(path)
		if err != nil {
			storeTemplate.err = err
			return
		}
		if err := s.Close(); err != nil {
			storeTemplate.err = err
			return
		}
		storeTemplate.bytes, storeTemplate.err = os.ReadFile(path)
	})
	return storeTemplate.bytes, storeTemplate.err
}

// seedStore writes a migrated empty store into dir unless one is there.
func seedStore(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return
	}
	raw, err := storeTemplateBytes()
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		t.Fatal("the store template is empty")
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// seededStorePath is a path to an empty migrated store in a new directory.
func seededStorePath(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	seedStore(t, dir, name)
	return filepath.Join(dir, name)
}

// seededDataDir is a new data directory holding an empty migrated store.
func seededDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seedStore(t, dir, "gateway.db")
	return dir
}
