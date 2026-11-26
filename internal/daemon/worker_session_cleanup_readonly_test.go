package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

// Go leaves its module cache read-only: files 0444 and folders 0555. Removing
// such a folder needs write permission on every folder first. This is the
// shape of tmp/agent-net-gomodcache inside an ended worker folder on a real
// machine.
func TestWorkerCleanupRemovesReadOnlyModuleCache(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	dir := filepath.Join(root, "workers", "w_ro")
	module := filepath.Join(dir, "tmp", "agent-net-gomodcache", "golang.org", "x", "exp@v0.0.0")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(module, "termlist.go")
	if err := os.WriteFile(source, []byte("package termlist\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o444); err != nil {
		t.Fatal(err)
	}
	for path := module; path != dir; path = filepath.Dir(path) {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
	}

	st, err := openState(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	disabled := false
	d := &Daemon{st: st, log: log.New(io.Discard, "", 0), cfg: Config{StateDir: root, Workers: WorkerSettings{DeleteSessions: &disabled}}}
	r := workerRow{ID: "w_ro", Harness: "claude", EndedReason: "done"}
	if err := st.saveWorker(r); err != nil {
		t.Fatal(err)
	}
	if !d.cleanupWorkerSessionRow(context.Background(), r) {
		t.Fatal("cleanup kept a worker whose folder holds a read-only module cache")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worker folder remains: %v", err)
	}
	rows, err := st.workers()
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}
