package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// newStateCleanupDaemon is a daemon with a real state database in a temporary
// folder and no running workers.
func newStateCleanupDaemon(t *testing.T) (*Daemon, *state, string) {
	t.Helper()
	root := t.TempDir()
	st, err := openState(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	d := &Daemon{st: st, cfg: Config{StateDir: root}, workers: map[string]*worker{}, log: log.New(io.Discard, "", 0)}
	return d, st, root
}

// writeWorkerState makes the private files of an ended worker: build state a
// worker can rebuild, and the files a resume or a later look needs.
func writeWorkerState(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"tmp/agent-net-gocache/aa", "tmp/opencode", "cache/uv", "data", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"tmp/agent-net-gocache/aa/entry", "tmp/opencode/log", "cache/uv/entry"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("build state"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"data/opencode.db", "worker.log", "session"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("kept"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerEndRemovesItsBuildState(t *testing.T) {
	d, _, root := newStateCleanupDaemon(t)
	dir := filepath.Join(root, "workers", "w_end")
	writeWorkerState(t, dir)
	w := &worker{id: "w_end", harness: "opencode", dir: dir, startedAt: time.Now(), ready: make(chan struct{})}
	d.workers[w.id] = w

	d.endWorker(w, protocol.StopHandback)

	for _, gone := range []string{"tmp", "cache"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("build state %s survived the end: %v", gone, err)
		}
	}
	// A hand-back can be resumed from its data folder, and a look at the end
	// reads the log. Both must stay.
	for _, kept := range []string{"data/opencode.db", "worker.log"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("kept file %s is gone: %v", kept, err)
		}
	}
}

func TestWorkerStateSweepCleansOnlyEndedWorkers(t *testing.T) {
	d, st, root := newStateCleanupDaemon(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour)

	// An ended hand-back keeps its data for a resume but loses its build state.
	resumable := filepath.Join(root, "workers", "w_resumable")
	writeWorkerState(t, resumable)
	if err := st.saveWorker(workerRow{ID: "w_resumable", Harness: "opencode", EndedReason: protocol.StopHandback, Resumable: true, Reported: true, StartedAt: old}); err != nil {
		t.Fatal(err)
	}
	// An ended worker that cannot be resumed and is past the record's own
	// retention frees its whole folder, as the record cleanup would.
	stopped := filepath.Join(root, "workers", "w_stopped")
	writeWorkerState(t, stopped)
	if err := st.saveWorker(workerRow{ID: "w_stopped", Harness: "claude", EndedReason: protocol.StopRequested, Reported: true, StartedAt: old}); err != nil {
		t.Fatal(err)
	}
	// A folder whose record is already gone is private state of no worker.
	orphan := filepath.Join(root, "workers", "w_orphan")
	writeWorkerState(t, orphan)
	// A running worker and a worker still starting are never touched.
	running := filepath.Join(root, "workers", "w_running")
	writeWorkerState(t, running)
	d.workers["w_running"] = &worker{id: "w_running"}
	starting := filepath.Join(root, "workers", "w_starting")
	writeWorkerState(t, starting)
	if err := st.saveWorker(workerRow{ID: "w_starting", Harness: "claude", StartedAt: now}); err != nil {
		t.Fatal(err)
	}

	d.cleanEndedWorkerState(now)

	for _, gone := range []string{
		filepath.Join(resumable, "tmp"),
		filepath.Join(resumable, "cache"),
		stopped,
		orphan,
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep: %v", gone, err)
		}
	}
	for _, kept := range []string{
		filepath.Join(resumable, "data", "opencode.db"),
		running,
		starting,
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

func TestCacheSizeCapCountsPerWorkerGoCache(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	d, _, root := newStateCleanupDaemon(t)
	shared := t.TempDir()
	d.cfg.WorkerGoCache = shared
	d.cfg.HomeDir = os.Getenv("HOME")
	writeGoCacheMarkers(t, shared)
	sharedNew := writeCacheEntry(t, shared, filepath.Join("aa", "new"), 1024, now.Add(-time.Hour))
	perWorker := filepath.Join(root, "workers", "w_sandboxed", "tmp", "agent-net-gocache")
	writeGoCacheMarkers(t, perWorker)
	perWorkerOld := writeCacheEntry(t, perWorker, filepath.Join("bb", "old"), 1024, now.Add(-20*time.Hour))

	freed := d.trimCacheSize(1500, 1200)
	if freed.Worker != 1024 || freed.Owner != 0 {
		t.Fatalf("freed worker=%d owner=%d, want worker=1024 owner=0", freed.Worker, freed.Owner)
	}
	if _, err := os.Stat(perWorkerOld); !os.IsNotExist(err) {
		t.Errorf("old per-worker cache entry was not trimmed: %v", err)
	}
	if _, err := os.Stat(sharedNew); err != nil {
		t.Errorf("new shared cache entry was trimmed: %v", err)
	}
}
