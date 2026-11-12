package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var updateMigrationsLock = flag.Bool("update-migrations-lock", false, "append new migrations to testdata/migrations.lock")

const (
	// migrationsLockPath is the lock relative to this package.
	migrationsLockPath = "testdata/migrations.lock"
	// migrationsLockRepoPath is the same file relative to the repository root.
	migrationsLockRepoPath = "internal/gateway/testdata/migrations.lock"
	// migrationsLockBaseRef is the default branch. Shipped migrations are on it.
	migrationsLockBaseRef = "origin/main"
)

// A shipped gateway records how many migrations it ran, so a migration must
// never change or move once released: a database at version N then runs the
// wrong migration N+1 (v0.4.46 inserted one in the middle and the gateway
// failed with "duplicate column name"). New migrations go at the end only.
// testdata/migrations.lock holds one hash per migration, in order.
func TestMigrationsAreAppendOnly(t *testing.T) {
	raw, err := os.ReadFile(migrationsLockPath)
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
		if err := writeLockFile("../..", migrationsLockBaseRef, migrationsLockRepoPath, hashes); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMigrationsLockExtendsBase is the check CI runs. The in-tree test above
// only agrees the list with the lock in the same tree, so a change that moved
// a shipped line and regenerated the lock would pass it. Comparing with the
// default branch closes that gap: the lock on disk may only append to it.
func TestMigrationsLockExtendsBase(t *testing.T) {
	skip, err := checkLockFile("../..", migrationsLockBaseRef, migrationsLockRepoPath)
	if skip != "" {
		t.Skip(skip)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsLockRefusesMovedMigration(t *testing.T) {
	dir := newLockRepo(t, "A", "B", "C")
	writeLockInTree(t, dir, "A", "X", "B", "C")
	skip, err := checkLockFile(dir, "main", migrationsLockRepoPath)
	if skip != "" {
		t.Fatalf("check skipped: %s", skip)
	}
	if err == nil {
		t.Fatal("a lock that inserts a line before shipped lines passed; want it refused")
	}
}

func TestMigrationsLockAcceptsAppendedMigration(t *testing.T) {
	dir := newLockRepo(t, "A", "B", "C")
	writeLockInTree(t, dir, "A", "B", "C", "X")
	skip, err := checkLockFile(dir, "main", migrationsLockRepoPath)
	if skip != "" {
		t.Fatalf("check skipped: %s", skip)
	}
	if err != nil {
		t.Fatalf("an appended line was refused: %v", err)
	}
}

func TestMigrationsLockUpdateRefusesMovedMigration(t *testing.T) {
	dir := newLockRepo(t, "A", "B", "C")
	if err := writeLockFile(dir, "main", migrationsLockRepoPath, []string{"A", "X", "B", "C"}); err == nil {
		t.Fatal("-update-migrations-lock wrote a lock that inserts a line before shipped lines; want it refused")
	}
	raw, err := os.ReadFile(filepath.Join(dir, migrationsLockRepoPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "A\nB\nC\n" {
		t.Fatalf("refused update still changed the lock to %q", raw)
	}
}

// newLockRepo makes a repository whose default branch ships the given lock lines.
func newLockRepo(t *testing.T, shipped ...string) string {
	t.Helper()
	dir := t.TempDir()
	lockGit(t, dir, "init", "-q", "-b", "main")
	writeLockInTree(t, dir, shipped...)
	lockGit(t, dir, "add", "-A")
	lockGit(t, dir, "commit", "-q", "-m", "ship migrations")
	return dir
}

// writeLockInTree writes the lock lines without committing them, as a change
// on disk would look before it is committed.
func writeLockInTree(t *testing.T, dir string, lines ...string) {
	t.Helper()
	path := filepath.Join(dir, migrationsLockRepoPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lockGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// checkLockFile compares the lock on disk at dir/rel with the copy at ref.
// skip explains why the check cannot run. A tarball, or a clone without the
// default branch, takes that path and does not fail.
func checkLockFile(dir, ref, rel string) (skip string, err error) {
	shipped, skip := shippedLockLines(dir, ref, rel)
	if skip != "" {
		return skip, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "", err
	}
	return "", lockAppendsOnly(shipped, strings.Fields(string(raw)))
}

// writeLockFile replaces the lock at dir/rel with hashes, but only when that
// is an append to the lock at ref. Without a readable ref it refuses: a write
// is an explicit request, so it must be checked.
func writeLockFile(dir, ref, rel string, hashes []string) error {
	shipped, skip := shippedLockLines(dir, ref, rel)
	if skip != "" {
		return fmt.Errorf("refusing to write %s: %s", rel, skip)
	}
	if err := lockAppendsOnly(shipped, hashes); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, rel), []byte(strings.Join(hashes, "\n")+"\n"), 0o644)
}

// shippedLockLines reads the lock as it is at ref. skip is set when git, the
// repository or the ref is missing.
func shippedLockLines(dir, ref, rel string) (lines []string, skip string) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "git is not installed"
	}
	out, err := exec.Command("git", "-C", dir, "show", ref+":"+rel).Output()
	if err != nil {
		return nil, fmt.Sprintf("%s has no %s (%v)", ref, rel, err)
	}
	return strings.Fields(string(out)), ""
}

// lockAppendsOnly fails unless next starts with every line of shipped, in order.
func lockAppendsOnly(shipped, next []string) error {
	if len(next) < len(shipped) {
		return fmt.Errorf("the lock has %d lines but %d are shipped: a shipped migration was removed", len(next), len(shipped))
	}
	for i, line := range shipped {
		if next[i] != line {
			return fmt.Errorf("line %d of the lock changed or moved: shipped migration %d must stay where it is; add new migrations at the end instead", i+1, i+1)
		}
	}
	return nil
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
