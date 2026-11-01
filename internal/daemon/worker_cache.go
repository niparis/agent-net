package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// openCacheRoot rejects symlink components and verifies the opened directory
// against the path, so a replaced cache root cannot redirect removal.
func openCacheRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, fmt.Errorf("invalid cache root: %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(abs)
	base := volume + string(filepath.Separator)
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(abs, base), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		info, err := root.Lstat(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, fmt.Errorf("cache path has a non-directory or symlink: %s", path)
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			root.Close()
			return nil, fmt.Errorf("cache path changed while opening: %s", path)
		}
		root.Close()
		root = next
	}
	return root, nil
}

// trimGoCache deletes old regular files inside the cache. Root confines all
// operations even if a cache entry is replaced by a symlink while walking.
func trimGoCache(path string, before time.Time) (int64, error) {
	root, err := openCacheRoot(path)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	var freed int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return root.Chmod(name, info.Mode().Perm()|0700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(before) || diskHardlinked(info) {
			return nil
		}
		if err := root.Chmod(name, info.Mode().Perm()|0200); err != nil {
			return err
		}
		if err := root.Remove(name); err != nil {
			return err
		}
		freed += info.Size()
		return nil
	})
	return freed, err
}

func trimGoTemp(path string, before time.Time, inUse func(string) (bool, error)) (int64, error) {
	root, err := openCacheRoot(path)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return freed, err
		}
		if !info.ModTime().Before(before) {
			continue
		}

		// A build can update a nested file without changing the top-level
		// directory mtime. Check every descendant before removing the entry.
		recent := errors.New("entry is still active")
		var size int64
		err = fs.WalkDir(root.FS(), entry.Name(), func(name string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			i, err := e.Info()
			if err != nil {
				return err
			}
			if !i.ModTime().Before(before) {
				return recent
			}
			if e.Type().IsRegular() {
				if diskHardlinked(i) {
					return fmt.Errorf("temporary entry contains a hard link: %s", name)
				}
				size += i.Size()
			}
			return nil
		})
		if errors.Is(err, recent) {
			continue
		}
		if err != nil {
			return freed, err
		}
		// A positive result protects the entry. Permission errors and other
		// inconclusive process checks do not prevent age-based cleanup.
		if used, _ := inUse(filepath.Join(path, entry.Name())); used {
			continue
		}
		err = fs.WalkDir(root.FS(), entry.Name(), func(name string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			i, err := e.Info()
			if err != nil {
				return err
			}
			// Recheck the age before changing permissions or deleting the entry.
			if !i.ModTime().Before(before) {
				return recent
			}
			if e.IsDir() {
				return root.Chmod(name, i.Mode().Perm()|0700)
			}
			if e.Type().IsRegular() {
				if diskHardlinked(i) {
					return fmt.Errorf("temporary entry contains a hard link: %s", name)
				}
				return root.Chmod(name, i.Mode().Perm()|0200)
			}
			return nil
		})
		if errors.Is(err, recent) {
			continue
		}
		if err != nil {
			return freed, err
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return freed, err
		}
		freed += size
	}
	return freed, nil
}

// trimWorkerCaches touches only configured directories.
func (d *Daemon) trimWorkerCaches(now time.Time) {
	before := now.Add(-2 * time.Hour)
	inUse := d.cfg.CacheEntryInUse
	if inUse == nil {
		inUse = processHasOpenPath
	}
	for _, path := range d.cfg.WorkerCacheDirs {
		n, err := trimGoCache(path, before)
		if d.log != nil {
			d.Logf("worker.cache_trim path=%q freed=%d error=%v", path, n, err)
		}
	}
	for _, path := range d.cfg.WorkerTempDirs {
		n, err := trimGoTemp(path, before, inUse)
		if d.log != nil {
			d.Logf("worker.temp_trim path=%q freed=%d error=%v", path, n, err)
		}
	}
}

func (d *Daemon) workerCacheLoop(ctx context.Context) {
	periodic := time.NewTicker(30 * time.Minute)
	defer periodic.Stop()
	pressure := time.NewTicker(15 * time.Second)
	defer pressure.Stop()
	d.workerCacheLoopTicks(ctx, periodic.C, pressure.C)
}

func (d *Daemon) workerCacheLoopTicks(ctx context.Context, periodic, pressure <-chan time.Time) {
	low := false
	var lastNotice, lastAttempt time.Time
	noticeResult := make(chan error, 1)
	pending := false
	var notice sync.WaitGroup
	defer notice.Wait()
	check := func() {
		p, hit := d.lowDisk()
		if !hit {
			low = false
			return
		}
		if !low {
			low = true
			d.trimWorkerCaches(time.Now())
		}
		if !pending && time.Since(lastNotice) >= time.Hour && time.Since(lastAttempt) >= 5*time.Minute {
			lastAttempt = time.Now()
			pending = true
			notice.Add(1)
			go func() { defer notice.Done(); noticeResult <- d.sendDiskNotice(ctx, p) }()
		}
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-periodic:
			d.trimWorkerCaches(time.Now())
		case <-pressure:
			check()
		case <-d.cacheWake:
			check()
		case err := <-noticeResult:
			pending = false
			if err == nil {
				lastNotice = time.Now()
			} else if d.log != nil {
				d.Logf("worker.disk_notice error=%v", err)
			}
		}
	}
}
