package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
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

func (d *Daemon) workerGoTmpDir() string {
	if d.cfg.WorkerGoTmp != "" {
		return d.cfg.WorkerGoTmp
	}
	if v := os.Getenv("AGENT_NET_GOTMPDIR"); v != "" {
		return v
	}
	return "/tmp/agent-net-gotmp"
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

// trimWorkerGoCache walks the worker Go build cache folder and removes old
// files whose mtime is older than maxAge (Go touches entries when used).
// README, trim.txt and the folder layout are preserved.
func (d *Daemon) trimWorkerGoCache(now time.Time, maxAge time.Duration) uint64 {
	if maxAge <= 0 {
		return 0
	}
	cacheDir := d.workerGoCacheDir()
	if isOwnerGoBuildCache(cacheDir, d.cfg.HomeDir) {
		if d.log != nil {
			d.Logf("worker cache: refuse to trim owner Go cache %s", cacheDir)
		}
		return 0
	}
	info, err := os.Stat(cacheDir)
	if err != nil || !info.IsDir() {
		return 0
	}
	var freed uint64
	_ = filepath.WalkDir(cacheDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil // Preserve the folder layout
		}
		name := entry.Name()
		if name == "README" || name == "trim.txt" {
			return nil
		}
		finfo, err := entry.Info()
		if err != nil {
			return nil
		}
		if now.Sub(finfo.ModTime()) > maxAge {
			size := uint64(finfo.Size())
			if err := os.Remove(path); err == nil {
				freed += size
			}
		}
		return nil
	})
	return freed
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

func goCacheBytes(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, ierr := entry.Info(); ierr == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
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

// trimCacheSize applies one size cap to the worker Go build cache and the
// owner's ~/.cache/go-build. When their combined size is over maxBytes it
// removes the oldest entries across both caches (Go touches an entry when it
// uses it) until the total is at or below lowBytes. The owner cache is trimmed
// by size only, never by age. README, trim.txt and the folder layout are kept.
func (d *Daemon) trimCacheSize(maxBytes, lowBytes int64) cacheTrimFreed {
	var freed cacheTrimFreed
	if maxBytes <= 0 {
		return freed
	}
	if lowBytes <= 0 || lowBytes >= maxBytes {
		lowBytes = maxBytes * 2 / 3
	}
	workerDir := d.workerGoCacheDir()
	ownerDir := ownerGoBuildCacheDir(d.cfg.HomeDir)
	var total int64
	var entries []goCacheEntry
	if isGoCacheDir(workerDir) {
		size, list := goCacheFiles(workerDir, false)
		total += size
		entries = append(entries, list...)
	}
	if ownerDir != "" && !sameDir(workerDir, ownerDir) && isGoCacheDir(ownerDir) {
		size, list := goCacheFiles(ownerDir, true)
		total += size
		entries = append(entries, list...)
	}
	if total <= maxBytes {
		return freed
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].mtime.Equal(entries[j].mtime) {
			return entries[i].path < entries[j].path
		}
		return entries[i].mtime.Before(entries[j].mtime)
	})
	for _, entry := range entries {
		if total <= lowBytes {
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
	}
	if d.log != nil {
		d.Logf("cache size trim: worker %d bytes freed, owner %d bytes freed (%d bytes left, cap %d, low %d)", freed.Worker, freed.Owner, total, maxBytes, lowBytes)
	}
	return freed
}

// cleanWorkerGoTmp removes subfolders of the worker test temp dir older than
// maxAge that no running build uses. Checking open file handles across builds is
// not checkable portably, so age limits and file mtimes inside each subfolder
// guard active builds.
func (d *Daemon) cleanWorkerGoTmp(now time.Time, maxAge time.Duration) uint64 {
	if maxAge <= 0 {
		return 0
	}
	tmpDir := d.workerGoTmpDir()
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

func (d *Daemon) fetchOpenInboxItems(ctx context.Context) ([]protocol.InboxItem, error) {
	raw, err := d.call(ctx, "inbox.list", struct{}{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Items, nil
}

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
	// Reserve the transition so concurrent checks cannot create duplicates.
	d.diskWarningUpdating = true
	active, itemID := d.diskWarningActive, d.diskWarningItemID
	d.diskWarnMu.Unlock()
	defer func() {
		d.diskWarnMu.Lock()
		d.diskWarningActive, d.diskWarningItemID = active, itemID
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
		d.trimCacheSize(settings.CacheMaxBytes, settings.CacheLowBytes)
		if active {
			return nil
		}
		if itemID == "" {
			if items, err := d.fetchOpenInboxItems(ctx); err == nil {
				for _, item := range items {
					if item.Kind == "check" && strings.HasPrefix(item.Text, "Disk space low on "+d.hostName()) && item.Status == "open" {
						itemID = item.ID
						active = true
						return nil
					}
				}
			}
		}
		workerDir := d.workerGoCacheDir()
		ownerDir := ownerGoBuildCacheDir(d.cfg.HomeDir)
		workerSize := goCacheBytes(workerDir)
		ownerSize := int64(0)
		ownerText := sizeText(0)
		if ownerDir != "" && sameDir(workerDir, ownerDir) {
			ownerText = "same as worker cache"
		} else if ownerDir != "" {
			ownerSize = goCacheBytes(ownerDir)
			ownerText = sizeText(ownerSize)
		}
		text := fmt.Sprintf("Disk space low on %s: %.0f%% used (worker Go cache %s, owner Go cache %s, total %s of %s cap)",
			d.hostName(), pct, sizeText(workerSize), ownerText, sizeText(workerSize+ownerSize), sizeText(settings.CacheMaxBytes))
		rec := "Free space by removing old worker files or enlarge the disk."
		raw, err := d.call(ctx, "inbox.create", map[string]any{
			"session_id":     d.cfg.NodeID,
			"kind":           "check",
			"text":           text,
			"recommendation": rec,
		})
		if err != nil {
			if d.log != nil {
				d.Logf("disk.warning create error: %v", err)
			}
			return err
		}
		var res struct {
			Item protocol.InboxItem `json:"item"`
		}
		if err := json.Unmarshal(raw, &res); err == nil && res.Item.ID != "" {
			itemID = res.Item.ID
		}
		active = true
		if d.log != nil {
			d.Logf("disk.warning raised: %.1f%% used", pct)
		}
		return nil
	}

	if pct < clearThreshold {
		if !active {
			return nil
		}
		active = false
		if itemID != "" {
			_, _ = d.call(ctx, "inbox.answer", map[string]string{
				"item_id": itemID,
				"answer":  "dismiss",
			})
		} else {
			if items, err := d.fetchOpenInboxItems(ctx); err == nil {
				for _, item := range items {
					if item.Kind == "check" && strings.HasPrefix(item.Text, "Disk space low on "+d.hostName()) && item.Status == "open" {
						_, _ = d.call(ctx, "inbox.answer", map[string]string{
							"item_id": item.ID,
							"answer":  "dismiss",
						})
					}
				}
			}
		}
		itemID = ""
		if d.log != nil {
			d.Logf("disk.warning cleared: %.1f%% used", pct)
		}
		return nil
	}

	return nil
}

func (d *Daemon) cleanWorkerCachesAndDisk(ctx context.Context, now time.Time) uint64 {
	snap := d.SettingsSnapshot()
	settings := d.workerSettingsWith(snap, "")
	cacheFreed := d.trimWorkerGoCache(now, settings.CacheMaxAge)
	sizeFreed := d.trimCacheSize(settings.CacheMaxBytes, settings.CacheLowBytes)
	tmpFreed := d.cleanWorkerGoTmp(now, settings.TmpMaxAge)
	totalFreed := cacheFreed + sizeFreed.total() + tmpFreed
	if d.log != nil {
		d.Logf("cache cleanup: %d bytes freed", totalFreed)
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
