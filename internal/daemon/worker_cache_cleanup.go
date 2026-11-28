package daemon

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	workerCacheCleanFirstDelay = time.Minute
	workerCacheCleanEvery      = time.Hour
)

func (d *Daemon) workerGoCacheDir() string {
	if d.cfg.WorkerGoCache != "" {
		return d.cfg.WorkerGoCache
	}
	if v := os.Getenv("AGENT_NET_GOCACHE"); v != "" {
		return v
	}
	return "/tmp/agent-net-gocache"
}

// explicitGoTmpDir is the Go temp folder the owner or a test named, or "".
func (d *Daemon) explicitGoTmpDir() string {
	if d.cfg.WorkerGoTmp != "" {
		return d.cfg.WorkerGoTmp
	}
	return os.Getenv("AGENT_NET_GOTMPDIR")
}

// workerGoTmpDir is the Go temp folder of unsandboxed workers: a RAM folder when
// /dev/shm has room, else the disk folder (see defaultGoTmpDir).
func (d *Daemon) workerGoTmpDir() string {
	if dir := d.explicitGoTmpDir(); dir != "" {
		return dir
	}
	return defaultGoTmpDir(d.shmNow())
}

// workerSandboxGoTmpDir is the Go temp folder of a worker whose sandbox mounts
// its own /dev (see sandboxHidesHostDev). The folder must exist inside that
// sandbox, so it is on disk, where the sandbox's write list binds it.
func (d *Daemon) workerSandboxGoTmpDir() string {
	if dir := d.explicitGoTmpDir(); dir != "" {
		return dir
	}
	return gotmpDiskDir
}

// workerGoTmpFor is the Go temp folder a worker's shell gets as GOTMPDIR.
func (d *Daemon) workerGoTmpFor(w *worker) string {
	if sandboxHidesHostDev(w.harness) {
		return d.workerSandboxGoTmpDir()
	}
	return d.workerGoTmpDir()
}

// workerRAMTmpDir is the TMPDIR a worker's shell gets when its harness keeps the
// host /dev/shm: the RAM folder when /dev/shm has room, else "". A named Go temp
// folder leaves TMPDIR as the daemon had it.
func (d *Daemon) workerRAMTmpDir() string {
	if d.explicitGoTmpDir() != "" || !shmHasRoom(d.shmNow()) {
		return ""
	}
	return gotmpShmTmpDir
}

func ownerGoBuildCacheDir(homeDir string) string {
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	if homeDir == "" {
		return ""
	}
	return filepath.Join(homeDir, ".cache", "go-build")
}

// sameDir tells whether two paths name the same folder, following symlinks.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	cleanA, cleanB := filepath.Clean(a), filepath.Clean(b)
	if cleanA == cleanB {
		return true
	}
	realA, err1 := filepath.EvalSymlinks(cleanA)
	realB, err2 := filepath.EvalSymlinks(cleanB)
	return err1 == nil && err2 == nil && realA == realB
}

func isOwnerGoBuildCache(cacheDir, homeDir string) bool {
	return sameDir(cacheDir, ownerGoBuildCacheDir(homeDir))
}

// goCacheEntry is one removable file of a Go build cache.
type goCacheEntry struct {
	path  string
	size  int64
	mtime time.Time
	owner bool
}

type cacheTrimFreed struct {
	Worker uint64
	Owner  uint64
}

func (f cacheTrimFreed) total() uint64 { return f.Worker + f.Owner }

// isGoCacheDir tells whether a folder holds a Go build cache. Go writes its
// README when it creates the cache, so a wrong path is never trimmed.
func isGoCacheDir(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "README"))
	return err == nil
}

// goCacheFiles counts a Go build cache and lists the files the daemon may
// remove. Files at the top level (README, trim.txt) and the folder layout are
// kept, so only files inside a hash folder are listed.
func goCacheFiles(dir string, owner bool) (int64, []goCacheEntry) {
	var total int64
	var entries []goCacheEntry
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, ierr := entry.Info()
		if ierr != nil {
			return nil
		}
		size := info.Size()
		total += size
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil || !strings.ContainsRune(rel, os.PathSeparator) {
			return nil
		}
		if entry.Name() == "README" || entry.Name() == "trim.txt" {
			return nil
		}
		entries = append(entries, goCacheEntry{path: path, size: size, mtime: info.ModTime(), owner: owner})
		return nil
	})
	return total, entries
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// goCacheSizeTrimFloor is the youngest entry the size trim may remove. Go
// refreshes a cache file's mtime only when it is about an hour old, so a file a
// running build is reading can look up to an hour old. Removing it would break
// that build with "no such file or directory" on import.
const goCacheSizeTrimFloor = 70 * time.Minute

// trimCache removes worker cache entries older than maxAge (when it is above
// zero) and applies one size cap to the worker Go build caches and the owner's
// ~/.cache/go-build. When their combined size is over maxBytes it removes the
// oldest entries across all of them (Go touches an entry when it uses it) until
// the total is at or below lowBytes. It never removes an entry younger than
// goCacheSizeTrimFloor: once only such entries are left, it stops above lowBytes
// and the log line says so. The owner cache is trimmed by size only, never by
// age. README, trim.txt and the folder layout are kept.
func (d *Daemon) trimCache(maxAge time.Duration, maxBytes, lowBytes int64) cacheTrimFreed {
	var freed cacheTrimFreed
	if maxAge <= 0 && maxBytes <= 0 {
		return freed
	}
	if maxBytes > 0 && (lowBytes <= 0 || lowBytes >= maxBytes) {
		lowBytes = maxBytes * 2 / 3
	}
	workerDir := d.workerGoCacheDir()
	ownerDir := ownerGoBuildCacheDir(d.cfg.HomeDir)
	if isOwnerGoBuildCache(workerDir, d.cfg.HomeDir) {
		if d.log != nil {
			d.Logf("worker cache: refuse to trim owner Go cache %s by age", workerDir)
		}
		workerDir = ""
	}
	var total int64
	var entries []goCacheEntry
	if isGoCacheDir(workerDir) {
		size, list := goCacheFiles(workerDir, false)
		total += size
		entries = append(entries, list...)
	}
	for _, dir := range d.workerPerWorkerGoCacheDirs() {
		if sameDir(dir, workerDir) || (ownerDir != "" && sameDir(dir, ownerDir)) {
			continue
		}
		size, list := goCacheFiles(dir, false)
		total += size
		entries = append(entries, list...)
	}
	if ownerDir != "" && isGoCacheDir(ownerDir) {
		size, list := goCacheFiles(ownerDir, true)
		total += size
		entries = append(entries, list...)
	}
	if total == 0 {
		return freed
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].mtime.Equal(entries[j].mtime) {
			return entries[i].path < entries[j].path
		}
		return entries[i].mtime.Before(entries[j].mtime)
	})
	now := time.Now()
	overCap := maxBytes > 0 && total > maxBytes
	stoppedForAge := false
	for _, entry := range entries {
		ageOld := maxAge > 0 && !entry.owner && now.Sub(entry.mtime) > maxAge
		wanted := ageOld || (overCap && total > lowBytes)
		if !wanted {
			// Sorted oldest first. An owner entry older than maxAge may still be
			// followed by a worker entry to age out, so scan on for one.
			if !overCap && (maxAge <= 0 || now.Sub(entry.mtime) <= maxAge) {
				break
			}
			continue
		}
		if now.Sub(entry.mtime) < goCacheSizeTrimFloor {
			if overCap && total > lowBytes {
				stoppedForAge = true
			}
			break
		}
		if err := os.Remove(entry.path); err != nil {
			continue
		}
		total -= entry.size
		if entry.owner {
			freed.Owner += uint64(entry.size)
		} else {
			freed.Worker += uint64(entry.size)
		}
		if total <= lowBytes {
			overCap = false
		}
	}
	if d.log != nil {
		line := fmt.Sprintf("cache trim: worker %d bytes freed, owner %d bytes freed (%d bytes left, cap %d, low %d)", freed.Worker, freed.Owner, total, maxBytes, lowBytes)
		if stoppedForAge {
			line += fmt.Sprintf("; stopped: the %d bytes left are in entries newer than 70m", total)
		}
		d.Logf("%s", line)
	}
	return freed
}

// orphanAgentNetTmpAge is how long an agent-net-* temp folder next to the Go
// temp folder must be idle before the sweep removes it. Sandboxes and test
// scripts make these folders; a sandbox can leave them read-only.
const orphanAgentNetTmpAge = 6 * time.Hour

// workerTrimRoot is the folder whose agent-net-* entries the sweep removes:
// /tmp, or the parent of a Go temp folder the owner named.
func (d *Daemon) workerTrimRoot() string {
	if d.cfg.testTrimRoot != "" {
		return d.cfg.testTrimRoot
	}
	if dir := d.explicitGoTmpDir(); dir != "" {
		return filepath.Dir(dir)
	}
	return filepath.Dir(gotmpDiskDir)
}

// cleanWorkerGoTmp removes subfolders of the worker test temp dir older than
// maxAge that no running build uses, and every other agent-net-* temp folder
// idle for six hours. Checking open file handles across builds is not checkable
// portably, so age limits and file mtimes inside each subfolder guard active
// builds.
func (d *Daemon) cleanWorkerGoTmp(now time.Time, maxAge time.Duration) uint64 {
	var freed uint64
	if maxAge > 0 {
		// Both folders can hold worker test runs: the RAM one for unsandboxed
		// workers and the disk one for sandboxed workers. The RAM TMPDIR folder
		// holds the temp files of the same runs, unless a Go temp folder is named.
		dirs := []string{d.workerGoTmpDir()}
		if sandbox := d.workerSandboxGoTmpDir(); sandbox != dirs[0] {
			dirs = append(dirs, sandbox)
		}
		if d.explicitGoTmpDir() == "" {
			dirs = append(dirs, gotmpShmTmpDir)
		}
		for _, dir := range dirs {
			freed += cleanGoTmpFolder(dir, now, maxAge)
		}
	}
	freed += d.cleanOrphanAgentNetTmp(now)
	if freed > 0 && d.log != nil {
		d.Logf("worker tmp cleanup: %d bytes freed", freed)
	}
	return freed
}

// cleanOrphanAgentNetTmp removes the agent-net-* folders under the trim root
// that no file was written in for six hours. It keeps the cache and the temp
// folders themselves by name, which can look idle too.
func (d *Daemon) cleanOrphanAgentNetTmp(now time.Time) uint64 {
	entries, err := os.ReadDir(d.workerTrimRoot())
	if err != nil {
		return 0
	}
	keep := []string{d.workerGoCacheDir(), d.workerGoTmpDir(), d.workerSandboxGoTmpDir(), gotmpShmTmpDir}
	var freed uint64
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agent-net-") {
			continue
		}
		dir := filepath.Join(d.workerTrimRoot(), entry.Name())
		taken := false
		for _, k := range keep {
			if k != "" && sameDir(dir, k) {
				taken = true
				break
			}
		}
		if taken || folderWrittenSince(dir, now.Add(-orphanAgentNetTmpAge)) {
			continue
		}
		size := uint64(folderBytes(dir))
		if err := removeWorkerFolder(dir); err == nil {
			freed += size
		}
	}
	return freed
}

// folderWrittenSince reports whether a file or folder under dir was written
// after since.
func folderWrittenSince(dir string, since time.Time) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(since) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// cleanGoTmpFolder removes the old subfolders of one Go temp folder (see
// cleanWorkerGoTmp).
func cleanGoTmpFolder(tmpDir string, now time.Time, maxAge time.Duration) uint64 {
	info, err := os.Stat(tmpDir)
	if err != nil || !info.IsDir() {
		return 0
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return 0
	}
	var freed uint64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(tmpDir, entry.Name())
		dirInfo, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(dirInfo.ModTime()) < maxAge {
			continue
		}
		hasNewer := false
		var dirSize uint64
		_ = filepath.WalkDir(subDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			finfo, err := d.Info()
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				dirSize += uint64(finfo.Size())
			}
			if now.Sub(finfo.ModTime()) < maxAge {
				hasNewer = true
				return filepath.SkipAll
			}
			return nil
		})
		if hasNewer {
			continue
		}
		if err := os.RemoveAll(subDir); err == nil {
			freed += dirSize
		}
	}
	return freed
}

func (d *Daemon) diskMaxUsagePct() (float64, bool) {
	if d.cfg.testDiskPercent != nil {
		return d.cfg.testDiskPercent("")
	}
	var maxPct float64
	var found bool
	for _, path := range []string{d.cfg.StateDir, d.workerGoCacheDir(), ownerGoBuildCacheDir(d.cfg.HomeDir)} {
		if path == "" {
			continue
		}
		probe := path
		for probe != "" && probe != "." && probe != "/" {
			if _, err := os.Stat(probe); err == nil {
				break
			}
			probe = filepath.Dir(probe)
		}
		if pct, ok := diskPercentUsed(probe); ok {
			if !found || pct > maxPct {
				maxPct = pct
			}
			found = true
		}
	}
	return maxPct, found
}

// diskIntakeSubject is the disk warning's intake subject. With the machine the
// gateway adds, it deduplicates one entry per machine.
const diskIntakeSubject = "disk"

func (d *Daemon) checkDiskWarning(ctx context.Context, now time.Time) error {
	pct, ok := d.diskMaxUsagePct()
	if !ok {
		return nil
	}
	d.diskWarnMu.Lock()
	if d.diskWarningUpdating {
		d.diskWarnMu.Unlock()
		return nil
	}
	// Reserve the transition so concurrent checks cannot report twice.
	d.diskWarningUpdating = true
	active, cleared := d.diskWarningActive, d.diskWarningCleared
	d.diskWarnMu.Unlock()
	defer func() {
		d.diskWarnMu.Lock()
		d.diskWarningActive, d.diskWarningCleared = active, cleared
		d.diskWarningUpdating = false
		d.diskWarnMu.Unlock()
	}()

	const (
		warnThreshold  = 85.0
		clearThreshold = 80.0
	)

	if pct >= warnThreshold {
		// Free space at once, not only at the next hourly cleanup.
		settings := d.workerSettings("")
		d.trimCache(settings.CacheMaxAge, settings.CacheMaxBytes, settings.CacheLowBytes)
		wasActive := active
		workerDir := d.workerGoCacheDir()
		ownerDir := ownerGoBuildCacheDir(d.cfg.HomeDir)
		workerSize := folderBytes(workerDir)
		for _, dir := range d.workerPerWorkerGoCacheDirs() {
			if !sameDir(dir, workerDir) {
				workerSize += folderBytes(dir)
			}
		}
		ownerSize := int64(0)
		ownerText := sizeText(0)
		if ownerDir != "" && sameDir(workerDir, ownerDir) {
			ownerText = "same as worker cache"
		} else if ownerDir != "" {
			ownerSize = folderBytes(ownerDir)
			ownerText = sizeText(ownerSize)
		}
		text := fmt.Sprintf("Disk space low on %s: %.0f%% used (worker Go cache %s, owner Go cache %s, total %s of %s cap)",
			d.hostName(), pct, sizeText(workerSize), ownerText, sizeText(workerSize+ownerSize), sizeText(settings.CacheMaxBytes))
		active, cleared = true, false
		if !wasActive && d.log != nil {
			d.Logf("disk.warning raised: %s", text)
		}
		return nil
	}

	if pct < clearThreshold && (active || !cleared) {
		if active && d.log != nil {
			d.Logf("disk.warning cleared: %.1f%% used", pct)
		}
		active, cleared = false, true
	}

	return nil
}

func (d *Daemon) cleanWorkerCachesAndDisk(ctx context.Context, now time.Time) uint64 {
	snap := d.SettingsSnapshot()
	settings := d.workerSettingsWith(snap, "")
	cacheFreed := d.trimCache(settings.CacheMaxAge, settings.CacheMaxBytes, settings.CacheLowBytes)
	tmpFreed := d.cleanWorkerGoTmp(now, settings.TmpMaxAge)
	stateFreed := d.cleanEndedWorkerState(now)
	totalFreed := cacheFreed.total() + tmpFreed + stateFreed
	if d.log != nil {
		d.Logf("cache cleanup: cache %d bytes freed, tmp %d bytes freed, state %d bytes freed, total %d bytes freed",
			cacheFreed.total(), tmpFreed, stateFreed, totalFreed)
	}
	_ = d.checkDiskWarning(ctx, now)
	return totalFreed
}

func (d *Daemon) workerCacheCleanupLoop(ctx context.Context) {
	first := workerCacheCleanFirstDelay
	if d.cfg.testWorkerCacheCleanFirst != 0 {
		first = d.cfg.testWorkerCacheCleanFirst
	}
	every := workerCacheCleanEvery
	if d.cfg.testWorkerCacheCleanEvery != 0 {
		every = d.cfg.testWorkerCacheCleanEvery
	}
	runEvery(ctx, first, every, func() {
		now := time.Now()
		d.cleanWorkerCachesAndDisk(ctx, now)
		d.pruneUVCache(ctx, now)
	})
}
