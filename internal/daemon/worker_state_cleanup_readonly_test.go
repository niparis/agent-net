package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// writeReadOnlyModuleTree makes the Go module cache shape inside dir: files
// 0444 and folders 0555, the permissions `go mod download` leaves behind.
func writeReadOnlyModuleTree(t *testing.T, dir string) {
	t.Helper()
	module := filepath.Join(dir, "golang.org", "x", "exp@v0.0.0-20240101")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"termlist.go", "go.mod"} {
		path := filepath.Join(module, name)
		if err := os.WriteFile(path, []byte("package termlist\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	for path := module; ; path = filepath.Dir(path) {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
		if path == dir {
			break
		}
	}
}

// A folder whose record is gone belongs to no worker, so the sweep frees it
// even when its tmp holds a read-only module cache.
func TestWorkerStateSweepRemovesOrphanFolderWithReadOnlyModuleCache(t *testing.T) {
	d, _, root := newStateCleanupDaemon(t)
	orphan := filepath.Join(root, "workers", "w_orphan")
	writeReadOnlyModuleTree(t, filepath.Join(orphan, "tmp", "agent-net-gomodcache"))

	d.cleanEndedWorkerState(time.Now())

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan folder survived the sweep: %v", err)
	}
}

// A resumable worker keeps its data folder but loses its build state, and its
// tmp can hold a read-only module cache too.
func TestWorkerStateSweepRemovesBuildStateWithReadOnlyModuleCache(t *testing.T) {
	d, st, root := newStateCleanupDaemon(t)
	dir := filepath.Join(root, "workers", "w_resumable")
	writeReadOnlyModuleTree(t, filepath.Join(dir, "tmp", "agent-net-gomodcache"))
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "data", "opencode.db")
	if err := os.WriteFile(kept, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.saveWorker(workerRow{ID: "w_resumable", Harness: "opencode", EndedReason: protocol.StopHandback, Resumable: true, Reported: true, StartedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	d.cleanEndedWorkerState(time.Now())

	if _, err := os.Stat(filepath.Join(dir, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("build state survived the sweep: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("data folder was removed: %v", err)
	}
}

// A worker that cannot be resumed loses its whole folder when its process
// ends, even when that folder holds a read-only module cache.
func TestWorkerEndRemovesReadOnlyModuleCacheFolder(t *testing.T) {
	d, _, root := newStateCleanupDaemon(t)
	dir := filepath.Join(root, "workers", "w_end")
	writeReadOnlyModuleTree(t, filepath.Join(dir, "tmp", "agent-net-gomodcache"))
	w := &worker{id: "w_end", harness: "claude", dir: dir, startedAt: time.Now(), ready: make(chan struct{})}
	d.workers[w.id] = w

	d.endWorker(w, protocol.StopRequested)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worker folder survived the end: %v", err)
	}
}
