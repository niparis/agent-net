package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
)

// Setup tests need isolated gateway contents, not a replay of every historical
// schema migration. The template must remain empty after a fixture writes to it.
func TestFixtureDatabaseTemplateIsIndependent(t *testing.T) {
	first := newFixture(t)
	seedPath := filepath.Join(os.Getenv("AGENT_NET_SETUP_TEST_ROOT"), "fixture-gateway.db")
	before, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	code, err := first.gw.Store().CreateEnrollCode(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second := newFixture(t)
	if valid, err := second.gw.Store().EnrollCodeValid(code); err != nil || valid {
		t.Fatalf("enrollment leaked between fixtures: valid=%v err=%v", valid, err)
	}
	if valid, err := first.gw.Store().EnrollCodeValid(code); err != nil || !valid {
		t.Fatalf("first fixture lost its enrollment: valid=%v err=%v", valid, err)
	}
	after, err := os.ReadFile(seedPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("fixture changed the template: %v", err)
	}
	for _, dir := range []string{first.data, second.data} {
		info, err := os.Stat(filepath.Join(dir, "gateway.db"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("fixture database permissions = %o", info.Mode().Perm())
		}
	}
	if first.gw.Fingerprint() == second.gw.Fingerprint() {
		t.Fatal("fixtures share a TLS identity")
	}
}

// Migrate an empty database once per package process. Close checkpoints the WAL
// before the bytes are cached. Each fixture gets an ordinary private copy and
// still opens its own store, so startup repairs and all setup behavior run.
// Keep this lazy: listing tests and tests without gateways need no database.
var fixtureDatabase = sync.OnceValues(func() ([]byte, error) {
	path := filepath.Join(os.Getenv("AGENT_NET_SETUP_TEST_ROOT"), "fixture-gateway.db")
	store, err := gateway.OpenStore(path)
	if err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

func copyFixtureDatabase(t *testing.T, dir string) {
	t.Helper()
	data, err := fixtureDatabase()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gateway.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
