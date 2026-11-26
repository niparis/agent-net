package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A worker's private folder holds two kinds of files: build state that any
// later run rebuilds, and state a resume, a continuation or the record's own
// cleanup reads. Build state is removed when the worker ends and by the sweep
// below, so an ended worker costs a few small files, not a box of caches.

// workerStateRoot is the folder of the private folders of this machine's
// workers.
func (d *Daemon) workerStateRoot() string {
	return filepath.Join(d.cfg.StateDir, "workers")
}

// workerBuildStateDirs are the folders under a worker folder that hold build
// state: the private TMPDIR that the OS sandbox also gives the worker's package
// and Go caches (its GOCACHE is /tmp/agent-net-gocache inside that sandbox),
// and the package caches of a worker without the sandbox. A resume makes them
// again when it starts the harness.
func workerBuildStateDirs(dir string) []string {
	return []string{filepath.Join(dir, "tmp"), filepath.Join(dir, "cache")}
}

// removeWorkerBuildState removes the build state of a worker folder and returns
// the bytes freed. Only real folders are removed: the daemon never follows a
// link out of the state folder.
func removeWorkerBuildState(dir string) uint64 {
	var freed uint64
	for _, path := range workerBuildStateDirs(dir) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		freed += uint64(workerFolderBytes(path))
		_ = removeWorkerFolder(path)
	}
	return freed
}

// cleanEndedWorkerBuildState removes the build state of a worker that just
// ended and keeps the rest of its folder: an OpenCode or Codex hand-back is
// resumed from its data folder, the record's own cleanup deletes the harness
// session through that folder, and the log is what a look at the end reads.
func (d *Daemon) cleanEndedWorkerBuildState(id, dir string) {
	if dir == "" {
		return
	}
	if freed := removeWorkerBuildState(dir); freed > 0 {
		d.Logf("worker.state.cleaned worker=%s bytes_freed=%d", id, freed)
	}
}

// cleanEndedWorkerState sweeps the private folders of workers that ended before
// this ran, including the ones that ended before the daemon removed their build
// state at the end. It removes build state from every folder that has no
// running worker, frees the whole folder of a worker that cannot be resumed and
// is past the record's retention, and leaves a resumable worker's data folder
// where a resume finds it. A worker that runs or is still starting is never
// touched, and neither is a folder that starts with any name but w_.
func (d *Daemon) cleanEndedWorkerState(now time.Time) uint64 {
	if d.st == nil {
		// Without the worker records this cannot prove that a folder belongs
		// to no running worker, so it touches nothing.
		return 0
	}
	entries, err := os.ReadDir(d.workerStateRoot())
	if err != nil {
		return 0
	}
	set := d.workerSettings("")
	live := map[string]bool{}
	ended := map[string]workerRow{}
	rows, err := d.st.workers()
	if err != nil {
		return 0
	}
	for _, r := range rows {
		if r.EndedReason == "" {
			live[r.ID] = true
		} else {
			ended[r.ID] = r
		}
	}
	d.wmu.Lock()
	for id := range d.workers {
		live[id] = true
	}
	d.wmu.Unlock()
	// With session deletion off, an OpenCode folder may hold the only copy of
	// its conversation, so a folder without a record is kept as well.
	deletionOff := set.DeleteSessions != nil && !*set.DeleteSessions

	var freed uint64
	folders := 0
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(id, "w_") || live[id] {
			continue
		}
		dir := filepath.Join(d.workerStateRoot(), id)
		row, hasRow := ended[id]
		if hasRow && row.Resumable {
			// A resume starts the same worker again from this folder. The
			// resume lock lets the sweep skip a folder whose resume is
			// starting, and holds a later resume until the removal is done.
			if !d.resumeMu.TryLock() {
				continue
			}
			bytes := removeWorkerBuildState(dir)
			d.resumeMu.Unlock()
			if bytes > 0 {
				freed += bytes
				folders++
			}
			continue
		}
		keepFolder := true
		switch {
		case hasRow && row.StartedAt.Add(set.MaxAge).After(now):
			// The record cleanup owns a recent ended worker, including the
			// harness session deletion that reads its data folder.
		case !hasRow && deletionOff:
			// The folder of a dropped record may be an OpenCode conversation.
		default:
			keepFolder = false
		}
		if keepFolder {
			if bytes := removeWorkerBuildState(dir); bytes > 0 {
				freed += bytes
				folders++
			}
			continue
		}
		freed += uint64(workerFolderBytes(dir))
		if err := removeWorkerFolder(dir); err != nil {
			continue
		}
		folders++
	}
	if folders > 0 {
		d.Logf("worker.state.sweep folders=%d bytes_freed=%d", folders, freed)
	}
	return freed
}

// workerPerWorkerGoCacheDirs lists the Go build caches that workers in the OS
// sandbox make in their private /tmp. They are part of the worker Go cache cap,
// not a second, uncapped cache.
func (d *Daemon) workerPerWorkerGoCacheDirs() []string {
	entries, err := os.ReadDir(d.workerStateRoot())
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "w_") {
			continue
		}
		dir := filepath.Join(d.workerStateRoot(), entry.Name(), "tmp", "agent-net-gocache")
		if isGoCacheDir(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}
