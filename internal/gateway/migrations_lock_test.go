package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"strings"
	"testing"
)

var updateMigrationsLock = flag.Bool("update-migrations-lock", false, "append new migrations to testdata/migrations.lock")

// A shipped gateway records how many migrations it ran, so a migration must
// never change or move once released: a database at version N then runs the
// wrong migration N+1 (v0.4.46 inserted one in the middle and the gateway
// failed with "duplicate column name"). New migrations go at the end only.
// testdata/migrations.lock holds one hash per migration, in order.
func TestMigrationsAreAppendOnly(t *testing.T) {
	const path = "testdata/migrations.lock"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	locked := strings.Fields(string(raw))
	hashes := make([]string, len(migrations))
	for i, m := range migrations {
		sum := sha256.Sum256([]byte(m))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	if len(hashes) < len(locked) {
		t.Fatalf("%d migrations, but %d are locked: a shipped migration was removed", len(hashes), len(locked))
	}
	for i, h := range locked {
		if hashes[i] != h {
			t.Fatalf("migration %d changed or moved; shipped migrations are append-only: add a new migration at the end instead", i+1)
		}
	}
	if len(hashes) > len(locked) {
		if !*updateMigrationsLock {
			t.Fatalf("%d new migration(s) at the end are not locked yet: run go test ./internal/gateway/ -run TestMigrationsAreAppendOnly -update-migrations-lock", len(hashes)-len(locked))
		}
		if err := os.WriteFile(path, []byte(strings.Join(hashes, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// migrationVersion finds the migration by its unique comment, so adding a
// later migration cannot make an upgrade test replay the wrong one.
func migrationVersion(t *testing.T, marker string) int {
	t.Helper()
	for i, migration := range migrations {
		if strings.Contains(migration, marker) {
			return i + 1
		}
	}
	t.Fatalf("migration %q not found", marker)
	return 0
}
