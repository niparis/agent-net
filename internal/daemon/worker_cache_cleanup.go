package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

func isOwnerGoBuildCache(cacheDir, homeDir string) bool {
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	if homeDir == "" {
		return false
	}
	ownerCache := filepath.Join(homeDir, ".cache", "go-build")
	cleanCache := filepath.Clean(cacheDir)
	cleanOwner := filepath.Clean(ownerCache)
	if cleanCache == cleanOwner {
		return true
	}
	realCache, err1 := filepath.EvalSymlinks(cleanCache)
	realOwner, err2 := filepath.EvalSymlinks(cleanOwner)
	if err1 == nil && err2 == nil && realCache == realOwner {
		return true
	}
	return false
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
	for _, path := range []string{d.cfg.StateDir, d.workerGoCacheDir()} {
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
		text := fmt.Sprintf("Disk space low on %s: %.0f%% used (daemon state or worker cache)", d.hostName(), pct)
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
	tmpFreed := d.cleanWorkerGoTmp(now, settings.TmpMaxAge)
	totalFreed := cacheFreed + tmpFreed
	if d.log != nil {
		d.Logf("worker cache cleanup: %d bytes freed", totalFreed)
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
		d.cleanWorkerCachesAndDisk(ctx, time.Now())
	})
}
