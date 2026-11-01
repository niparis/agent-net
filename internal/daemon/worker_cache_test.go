package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrimCacheOldFilesOnly(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	fresh := filepath.Join(root, "fresh")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("1234"), 0400); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	freed, err := trimGoCache(root, time.Now().Add(-2*time.Hour))
	if err != nil || freed != 4 {
		t.Fatalf("trim: %d %v", freed, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file remains: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file removed: %v", err)
	}
}

func TestTrimTempSkipsOpenEntries(t *testing.T) {
	root := t.TempDir()
	past := time.Now().Add(-3 * time.Hour)
	for _, name := range []string{"closed", "open"} {
		p := filepath.Join(root, name)
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "file"), []byte("1234"), 0400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	freed, err := trimGoTemp(root, time.Now().Add(-2*time.Hour), func(path string) (bool, error) { return filepath.Base(path) == "open", nil })
	if err != nil || freed != 4 {
		t.Fatalf("trim: %d %v", freed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "closed")); !os.IsNotExist(err) {
		t.Fatalf("closed entry remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "open")); err != nil {
		t.Fatalf("open entry removed: %v", err)
	}
}

func TestCacheSettings(t *testing.T) {
	d := t.TempDir()
	temp := t.TempDir()
	nc := NodeConfig{Workers: &NodeWorkers{CacheDirs: []string{d}, TempDirs: []string{temp}}}
	caches, temps, err := nc.WorkerCachePaths()
	if err != nil || len(caches) != 1 || caches[0] != d || len(temps) != 1 || temps[0] != temp {
		t.Fatalf("cache settings: %v %v %v", caches, temps, err)
	}
}

func TestCacheMaintenance(t *testing.T) {
	cache := t.TempDir()
	tmp := t.TempDir()
	old := time.Now().Add(-3 * time.Hour)
	for _, dir := range []string{cache, tmp} {
		p := filepath.Join(dir, "old")
		if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	d := &Daemon{cfg: Config{WorkerCacheDirs: []string{cache}, WorkerTempDirs: []string{tmp}, CacheEntryInUse: func(string) (bool, error) { return false, nil }}}
	d.trimWorkerCaches(time.Now())
	for _, dir := range []string{cache, tmp} {
		if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
			t.Fatalf("old entry in %s: %v", dir, err)
		}
	}
}

func TestDiskLargestFolders(t *testing.T) {
	root := t.TempDir()
	for name, size := range map[string]int{"one": 1, "three": 3, "two": 2, "four": 4} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file"), make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := largestDiskFolders(root)
	if len(got) != 3 || filepath.Base(got[0].path) != "four" || filepath.Base(got[1].path) != "three" || filepath.Base(got[2].path) != "two" {
		t.Fatalf("largest: %+v", got)
	}
}

func TestDiskNoticeIncludesFolders(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "large")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	var text string
	d := &Daemon{cfg: Config{DiskRoot: func(string) string { return root }, DiskNotice: func(_ context.Context, s string) error { text = s; return nil }}}
	if err := d.sendDiskNotice(context.Background(), diskPressure{path: root, free: 6 << 30, floor: 15}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "6 GB free") || !strings.Contains(text, dir) {
		t.Fatalf("notice: %q", text)
	}
}

func TestTrimCacheRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	old := filepath.Join(outside, "old")
	if err := os.WriteFile(old, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(old, when, when); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "cache")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := trimGoCache(link, time.Now().Add(-2*time.Hour)); err == nil {
		t.Fatal("accepted symlink root")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("outside file removed: %v", err)
	}
}

func TestDiskFreeMissingCachePath(t *testing.T) {
	root := t.TempDir()
	free, err := diskFree(filepath.Join(root, "missing", "cache"))
	if err != nil || free == 0 {
		t.Fatalf("missing cache disk: %d %v", free, err)
	}
}

func TestCacheSettingsRejectOverlap(t *testing.T) {
	root := t.TempDir()
	nc := NodeConfig{Workers: &NodeWorkers{CacheDirs: []string{root}, TempDirs: []string{filepath.Join(root, "nested")}}}
	if _, _, err := nc.WorkerCachePaths(); err == nil {
		t.Fatal("accepted overlapping cache and temp paths")
	}
}

func TestTrimCacheReadOnlyDirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "readonly")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "old")
	if err := os.WriteFile(file, []byte("x"), 0400); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0700) })
	if _, err := trimGoCache(root, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("old file remains: %v", err)
	}
}

func TestCacheLoopTrimWhenLow(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "old")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d := &Daemon{cacheWake: make(chan struct{}, 1), cfg: Config{StateDir: root, WorkerCacheDirs: []string{cache}, DiskFree: func(string) (uint64, error) { return 6 << 30, nil }, DiskRoot: func(string) string { return root }, DiskNotice: func(context.Context, string) error { return nil }}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.workerCacheLoopTicks(ctx, make(chan time.Time), make(chan time.Time)) }()
	eventually(t, "low disk cache trim", func() bool { _, err := os.Stat(path); return os.IsNotExist(err) })
	cancel()
	<-done
}

func TestTrimCacheDoesNotChmodOutsideHardlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "file")
	if err := os.WriteFile(file, []byte("x"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	_, _ = trimGoCache(root, time.Now().Add(-2*time.Hour))
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatalf("outside file changed: %v %v", info, err)
	}
}

func TestCacheLoopPeriodicTrim(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "old")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), WorkerCacheDirs: []string{cache}, DiskFree: func(string) (uint64, error) { return 20 << 30, nil }}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	periodic := make(chan time.Time, 1)
	pressure := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); d.workerCacheLoopTicks(ctx, periodic, pressure) }()
	periodic <- time.Now()
	eventually(t, "periodic cache trim", func() bool { _, err := os.Stat(path); return os.IsNotExist(err) })
	cancel()
	<-done
}

func TestCacheRootRejectsFilesystemRoot(t *testing.T) {
	root, err := openCacheRoot(string(filepath.Separator))
	if root != nil {
		root.Close()
	}
	if err == nil {
		t.Fatal("filesystem root accepted for cleanup")
	}
}

func TestCacheSettingsCanDisableDefaults(t *testing.T) {
	nc := NodeConfig{Workers: &NodeWorkers{CacheDirs: []string{}, TempDirs: []string{}}}
	cache, tmp, err := nc.WorkerCachePaths()
	if err != nil || len(cache) != 0 || len(tmp) != 0 {
		t.Fatalf("disabled paths: %v %v %v", cache, tmp, err)
	}
}

func TestCacheSettingsDisabledSurviveJSONRoundTrip(t *testing.T) {
	nc := NodeConfig{Workers: &NodeWorkers{CacheDirs: []string{}, TempDirs: []string{}}}
	data, err := json.Marshal(nc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NodeConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	cache, tmp, err := decoded.WorkerCachePaths()
	if err != nil || len(cache) != 0 || len(tmp) != 0 {
		t.Fatalf("disabled paths lost after JSON: %v %v %v", cache, tmp, err)
	}
}
