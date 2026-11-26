package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// writeCacheEntry creates one Go cache entry file of size bytes with an mtime.
func writeCacheEntry(t *testing.T, dir, name string, size int, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGoCacheMarkers(t *testing.T, dir string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README")
	trim := filepath.Join(dir, "trim.txt")
	if err := os.WriteFile(readme, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trim, nil, 0644); err != nil {
		t.Fatal(err)
	}
	return readme, trim
}

func TestCacheSizeCapDefaults(t *testing.T) {
	s := WorkerSettings{}.filled()
	if s.CacheMaxBytes != 15<<30 {
		t.Errorf("cache_max_bytes default = %d, want %d", s.CacheMaxBytes, int64(15)<<30)
	}
	if s.CacheLowBytes != 10<<30 {
		t.Errorf("cache_low_bytes default = %d, want %d", s.CacheLowBytes, int64(10)<<30)
	}
}

func TestCacheSizeCapTrimsOldestAcrossCaches(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	workerDir := t.TempDir()
	homeDir := t.TempDir()
	ownerDir := filepath.Join(homeDir, ".cache", "go-build")
	workerReadme, workerTrim := writeGoCacheMarkers(t, workerDir)
	ownerReadme, ownerTrim := writeGoCacheMarkers(t, ownerDir)

	workerOld := writeCacheEntry(t, workerDir, filepath.Join("aa", "old"), 1024, now.Add(-10*time.Hour))
	workerNew := writeCacheEntry(t, workerDir, filepath.Join("bb", "new"), 1024, now.Add(-1*time.Hour))
	ownerOld := writeCacheEntry(t, ownerDir, filepath.Join("cc", "old"), 1024, now.Add(-20*time.Hour))
	ownerMid := writeCacheEntry(t, ownerDir, filepath.Join("dd", "mid"), 1024, now.Add(-5*time.Hour))
	ownerNew := writeCacheEntry(t, ownerDir, filepath.Join("ee", "new"), 1024, now.Add(-30*time.Minute))

	var logs bytes.Buffer
	d := &Daemon{
		cfg: Config{HomeDir: homeDir, WorkerGoCache: workerDir},
		log: log.New(&logs, "", 0),
	}

	// 5 KiB total, cap 3 KiB, low mark 2 KiB. The three oldest files across
	// both caches go, and the two youngest stay.
	freed := d.trimCache(0, 3*1024, 2*1024)
	if freed.Worker != 1024 || freed.Owner != 2048 {
		t.Fatalf("freed worker=%d owner=%d, want worker=1024 owner=2048", freed.Worker, freed.Owner)
	}
	for _, gone := range []string{ownerOld, workerOld, ownerMid} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat error: %v", gone, err)
		}
	}
	for _, kept := range []string{workerNew, ownerNew, workerReadme, workerTrim, ownerReadme, ownerTrim} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept: %v", kept, err)
		}
	}
	// Folder layout is kept even when a folder becomes empty.
	for _, dir := range []string{filepath.Join(workerDir, "aa"), filepath.Join(ownerDir, "cc")} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("cache folder %s should be kept: %v", dir, err)
		}
	}
	line := logs.String()
	if !strings.Contains(line, "worker 1024 bytes freed") || !strings.Contains(line, "owner 2048 bytes freed") {
		t.Fatalf("log line %q does not name bytes freed per cache", line)
	}
}

func TestCacheSizeCapKeepsEntriesYoungerThan70Minutes(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	d, _, root := newStateCleanupDaemon(t)
	shared := t.TempDir()
	homeDir := t.TempDir()
	ownerDir := filepath.Join(homeDir, ".cache", "go-build")
	perWorker := filepath.Join(root, "workers", "w_active", "tmp", "agent-net-gocache")
	for _, dir := range []string{shared, ownerDir, perWorker} {
		writeGoCacheMarkers(t, dir)
	}

	// Go refreshes a cache file's mtime only when it is about an hour old, so a
	// file a running build is reading can look 65 minutes old. A 90 minute old
	// entry is safe to remove in every cache.
	sharedYoung := writeCacheEntry(t, shared, filepath.Join("aa", "young"), 1024, now.Add(-65*time.Minute))
	perWorkerYoung := writeCacheEntry(t, perWorker, filepath.Join("bb", "young"), 1024, now.Add(-65*time.Minute))
	ownerYoung := writeCacheEntry(t, ownerDir, filepath.Join("cc", "young"), 1024, now.Add(-65*time.Minute))
	sharedOld := writeCacheEntry(t, shared, filepath.Join("dd", "old"), 1024, now.Add(-90*time.Minute))
	perWorkerOld := writeCacheEntry(t, perWorker, filepath.Join("ee", "old"), 1024, now.Add(-90*time.Minute))
	ownerOld := writeCacheEntry(t, ownerDir, filepath.Join("ff", "old"), 1024, now.Add(-90*time.Minute))

	var logs bytes.Buffer
	d.cfg.WorkerGoCache = shared
	d.cfg.HomeDir = homeDir
	d.log = log.New(&logs, "", 0)

	// 6 KiB total, cap 5 KiB, low mark 2 KiB. The three 90 minute old entries
	// go. The trim then stops above the low mark, because only younger entries
	// are left, and the log line says so.
	freed := d.trimCache(0, 5*1024, 2*1024)
	if freed.Worker != 2048 || freed.Owner != 1024 {
		t.Fatalf("freed worker=%d owner=%d, want worker=2048 owner=1024", freed.Worker, freed.Owner)
	}
	for _, gone := range []string{sharedOld, perWorkerOld, ownerOld} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat error: %v", gone, err)
		}
	}
	for _, kept := range []string{sharedYoung, perWorkerYoung, ownerYoung} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s is younger than 70 minutes and should be kept: %v", kept, err)
		}
	}
	line := logs.String()
	if !strings.Contains(line, "stopped") || !strings.Contains(line, "newer than 70m") {
		t.Fatalf("log line %q does not say the trim stopped for young entries", line)
	}
}

func TestCacheSizeCapLeavesCachesUnderTheCap(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	workerDir := t.TempDir()
	homeDir := t.TempDir()
	ownerDir := filepath.Join(homeDir, ".cache", "go-build")
	writeGoCacheMarkers(t, workerDir)
	writeGoCacheMarkers(t, ownerDir)
	workerFile := writeCacheEntry(t, workerDir, filepath.Join("aa", "entry"), 512, now.Add(-48*time.Hour))
	ownerFile := writeCacheEntry(t, ownerDir, filepath.Join("bb", "entry"), 512, now.Add(-48*time.Hour))

	d := &Daemon{cfg: Config{HomeDir: homeDir, WorkerGoCache: workerDir}}
	freed := d.trimCache(0, 4*1024, 2*1024)
	if freed.Worker != 0 || freed.Owner != 0 {
		t.Fatalf("trimmed under the cap: %+v", freed)
	}
	for _, kept := range []string{workerFile, ownerFile} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept under the cap: %v", kept, err)
		}
	}
}

func TestCacheSizeCapSkipsOwnerFolderWithoutReadme(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	workerDir := t.TempDir()
	homeDir := t.TempDir()
	ownerDir := filepath.Join(homeDir, ".cache", "go-build")
	writeGoCacheMarkers(t, workerDir)
	ownerFile := writeCacheEntry(t, ownerDir, filepath.Join("aa", "not-a-cache"), 1024, now.Add(-48*time.Hour))
	workerFile := writeCacheEntry(t, workerDir, filepath.Join("bb", "entry"), 1024, now.Add(-2*time.Hour))

	d := &Daemon{cfg: Config{HomeDir: homeDir, WorkerGoCache: workerDir}}
	freed := d.trimCache(0, 512, 256)
	if _, err := os.Stat(ownerFile); err != nil {
		t.Fatalf("owner folder without README was trimmed: %v", err)
	}
	if _, err := os.Stat(workerFile); !os.IsNotExist(err) {
		t.Fatalf("worker entry over the cap should be removed, stat error: %v", err)
	}
	if freed.Worker != 1024 || freed.Owner != 0 {
		t.Fatalf("freed worker=%d owner=%d, want worker=1024 owner=0", freed.Worker, freed.Owner)
	}
}

func TestDiskWarningTrimsCachesAndNamesTheirSizes(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	workerDir := t.TempDir()
	homeDir := t.TempDir()
	ownerDir := filepath.Join(homeDir, ".cache", "go-build")
	writeGoCacheMarkers(t, workerDir)
	writeGoCacheMarkers(t, ownerDir)
	writeCacheEntry(t, workerDir, filepath.Join("aa", "old"), 1024, now.Add(-10*time.Hour))
	workerNew := writeCacheEntry(t, workerDir, filepath.Join("bb", "new"), 1024, now.Add(-1*time.Hour))
	ownerOld := writeCacheEntry(t, ownerDir, filepath.Join("cc", "old"), 1024, now.Add(-20*time.Hour))

	var reports []protocol.IntakeReport
	d := &Daemon{
		cfg: Config{
			NodeID:        "n_test",
			Hostname:      "test-node",
			HomeDir:       homeDir,
			WorkerGoCache: workerDir,
			Workers:       WorkerSettings{CacheMaxBytes: 2048, CacheLowBytes: 1024},
			testGatewayCall: func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
				if typ == "intake.report" {
					reports = append(reports, params.(protocol.IntakeReport))
					return nil, true, nil
				}
				return nil, false, nil
			},
			testDiskPercent: func(string) (float64, bool) { return 90, true },
		},
	}

	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected one warning, got %d", len(reports))
	}
	if reports[0].Kind != protocol.IntakeDiskSpace || reports[0].Subject != diskIntakeSubject || reports[0].Resolved {
		t.Fatalf("warning report %+v", reports[0])
	}
	for _, want := range []string{"worker Go cache 1.0 KB", "owner Go cache 0 bytes"} {
		if !strings.Contains(reports[0].Text, want) {
			t.Errorf("warning %q does not contain %q", reports[0].Text, want)
		}
	}
	// The warning fires the size trim at once: the oldest file across both
	// caches goes, whichever cache it is in.
	if _, err := os.Stat(ownerOld); !os.IsNotExist(err) {
		t.Errorf("owner cache was not trimmed by the warning: %v", err)
	}
	if _, err := os.Stat(workerNew); err != nil {
		t.Errorf("newest cache entry should be kept: %v", err)
	}
}

func TestWorkerCacheCleanupAgeRules(t *testing.T) {
	isolateDaemonCaches(t)
	cacheDir := t.TempDir()
	tmpDir := t.TempDir()

	// Cache folder layout
	sub01 := filepath.Join(cacheDir, "01")
	sub02 := filepath.Join(cacheDir, "02")
	if err := os.MkdirAll(sub01, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub02, 0755); err != nil {
		t.Fatal(err)
	}

	readmeFile := filepath.Join(cacheDir, "README")
	trimFile := filepath.Join(cacheDir, "trim.txt")
	oldCacheFile := filepath.Join(sub01, "old-entry")
	youngCacheFile := filepath.Join(sub02, "young-entry")

	for _, p := range []string{readmeFile, trimFile, oldCacheFile, youngCacheFile} {
		if err := os.WriteFile(p, []byte("cache-data-bytes"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	oldTime := now.Add(-30 * time.Hour)  // > 24h
	youngTime := now.Add(-2 * time.Hour) // < 24h

	_ = os.Chtimes(readmeFile, oldTime, oldTime)
	_ = os.Chtimes(trimFile, oldTime, oldTime)
	_ = os.Chtimes(oldCacheFile, oldTime, oldTime)
	_ = os.Chtimes(youngCacheFile, youngTime, youngTime)

	// Worker test temp folder layout
	oldSub := filepath.Join(tmpDir, "sub_old")
	youngSub := filepath.Join(tmpDir, "sub_young")
	activeSub := filepath.Join(tmpDir, "sub_active")

	for _, d := range []string{oldSub, youngSub, activeSub} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	oldSubFile := filepath.Join(oldSub, "test.bin")
	youngSubFile := filepath.Join(youngSub, "test.bin")
	activeSubOldFile := filepath.Join(activeSub, "old.bin")
	activeSubNewFile := filepath.Join(activeSub, "active.bin")

	for _, p := range []string{oldSubFile, youngSubFile, activeSubOldFile, activeSubNewFile} {
		if err := os.WriteFile(p, []byte("temp-data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	gotmpOldTime := now.Add(-10 * time.Hour)  // > 6h
	gotmpYoungTime := now.Add(-1 * time.Hour) // < 6h

	_ = os.Chtimes(oldSubFile, gotmpOldTime, gotmpOldTime)
	_ = os.Chtimes(oldSub, gotmpOldTime, gotmpOldTime)

	_ = os.Chtimes(youngSubFile, gotmpYoungTime, gotmpYoungTime)
	_ = os.Chtimes(youngSub, gotmpYoungTime, gotmpYoungTime)

	_ = os.Chtimes(activeSubOldFile, gotmpOldTime, gotmpOldTime)
	_ = os.Chtimes(activeSubNewFile, gotmpYoungTime, gotmpYoungTime)
	_ = os.Chtimes(activeSub, gotmpOldTime, gotmpOldTime)

	d := &Daemon{
		cfg: Config{
			WorkerGoCache: cacheDir,
			WorkerGoTmp:   tmpDir,
		},
	}

	freed := d.cleanWorkerCachesAndDisk(context.Background(), now)
	if freed == 0 {
		t.Fatalf("expected freed bytes > 0, got 0")
	}

	// Verify cache:
	// README, trim.txt and directory layout must be preserved.
	if _, err := os.Stat(readmeFile); err != nil {
		t.Errorf("README should be preserved: %v", err)
	}
	if _, err := os.Stat(trimFile); err != nil {
		t.Errorf("trim.txt should be preserved: %v", err)
	}
	if _, err := os.Stat(sub01); err != nil {
		t.Errorf("cache subdirectory 01 should be preserved: %v", err)
	}
	if _, err := os.Stat(sub02); err != nil {
		t.Errorf("cache subdirectory 02 should be preserved: %v", err)
	}
	// Old cache file removed
	if _, err := os.Stat(oldCacheFile); !os.IsNotExist(err) {
		t.Errorf("oldCacheFile should be removed: %v", err)
	}
	// Young cache file preserved
	if _, err := os.Stat(youngCacheFile); err != nil {
		t.Errorf("youngCacheFile should be preserved: %v", err)
	}

	// Verify gotmp:
	// oldSub removed
	if _, err := os.Stat(oldSub); !os.IsNotExist(err) {
		t.Errorf("oldSub should be removed: %v", err)
	}
	// youngSub preserved
	if _, err := os.Stat(youngSub); err != nil {
		t.Errorf("youngSub should be preserved: %v", err)
	}
	// activeSub (contains recently modified file) preserved
	if _, err := os.Stat(activeSub); err != nil {
		t.Errorf("activeSub should be preserved: %v", err)
	}
}

func TestWorkerCacheCleanupNeverTouchOwnerGoBuild(t *testing.T) {
	isolateDaemonCaches(t)
	homeDir := t.TempDir()
	ownerCache := filepath.Join(homeDir, ".cache", "go-build")
	if err := os.MkdirAll(filepath.Join(ownerCache, "01"), 0755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(ownerCache, "01", "dont-delete")
	if err := os.WriteFile(victim, []byte("important"), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldTime := now.Add(-100 * time.Hour)
	_ = os.Chtimes(victim, oldTime, oldTime)

	d := &Daemon{
		cfg: Config{
			HomeDir:       homeDir,
			WorkerGoCache: ownerCache,
		},
	}

	freed := d.trimCache(24*time.Hour, 0, 0)
	if freed.total() != 0 {
		t.Fatalf("expected 0 freed from owner cache, got %d", freed.total())
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim file in owner cache was deleted: %v", err)
	}
}

func TestWorkerCacheCleanupOffSwitch(t *testing.T) {
	isolateDaemonCaches(t)
	cacheDir := t.TempDir()
	tmpDir := t.TempDir()

	sub := filepath.Join(cacheDir, "01")
	_ = os.MkdirAll(sub, 0755)
	cacheFile := filepath.Join(sub, "old-entry")
	_ = os.WriteFile(cacheFile, []byte("cache-data"), 0644)

	gotmpSub := filepath.Join(tmpDir, "sub_old")
	_ = os.MkdirAll(gotmpSub, 0755)
	_ = os.WriteFile(filepath.Join(gotmpSub, "f"), []byte("data"), 0644)

	now := time.Now()
	oldTime := now.Add(-50 * time.Hour)
	_ = os.Chtimes(cacheFile, oldTime, oldTime)
	_ = os.Chtimes(gotmpSub, oldTime, oldTime)

	d := &Daemon{
		cfg: Config{
			WorkerGoCache: cacheDir,
			WorkerGoTmp:   tmpDir,
			Workers: WorkerSettings{
				CacheMaxAge: -1, // 0 / off
				TmpMaxAge:   -1, // 0 / off
			},
		},
	}

	freed := d.cleanWorkerCachesAndDisk(context.Background(), now)
	if freed != 0 {
		t.Fatalf("expected 0 freed with off switch, got %d", freed)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("cacheFile should be preserved when off: %v", err)
	}
	if _, err := os.Stat(gotmpSub); err != nil {
		t.Errorf("gotmpSub should be preserved when off: %v", err)
	}
}

// Use a real link: testGatewayCall bypasses the mutex acquisition in call.
func TestDiskWarningGatewayNoDeadlock(t *testing.T) {
	for _, clear := range []bool{false, true} {
		name := "Raise"
		if clear {
			name = "Clear"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := startGateway(t, t.TempDir(), "")
			n := startNodeCfg(t, g, nodeOpts{}, func(cfg *Config) {
				cfg.testDiskPercent = func(string) (float64, bool) {
					if clear {
						return 70, true
					}
					return 90, true
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if clear {
				// Leave an open warning for the clear pass to resolve.
				_, err := n.d.call(ctx, "intake.report", protocol.IntakeReport{Kind: protocol.IntakeDiskSpace, Subject: diskIntakeSubject, Text: "Disk space low"})
				if err != nil {
					t.Fatal(err)
				}
				n.d.diskWarningActive = true
			}
			done := make(chan error, 1)
			go func() { done <- n.d.checkDiskWarning(ctx, time.Now()) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("checkDiskWarning did not return within 2 seconds")
			}
			// A subsequent gateway call must still work after either transition.
			raw, err := n.d.call(ctx, protocol.FrameIntakeFind, protocol.IntakeFindArgs{})
			if err != nil {
				t.Fatal(err)
			}
			var found protocol.IntakeFindResult
			if err := json.Unmarshal(raw, &found); err != nil {
				t.Fatal(err)
			}
			want := 1
			if clear {
				want = 0
			}
			if found.Total != want {
				t.Fatalf("intake after transition: %s, want %d open entries", raw, want)
			}
		})
	}
}

func TestDiskWarningHysteresis(t *testing.T) {
	isolateDaemonCaches(t)
	var reports []protocol.IntakeReport
	var simPct float64

	d := &Daemon{
		cfg: Config{
			NodeID:   "n_test",
			Hostname: "test-node",
			StateDir: t.TempDir(),
			testGatewayCall: func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
				if typ == "intake.report" {
					reports = append(reports, params.(protocol.IntakeReport))
					return nil, true, nil
				}
				return nil, false, nil
			},
			testDiskPercent: func(path string) (float64, bool) { return simPct, true },
		},
	}
	now := time.Now()
	check := func(pct float64) {
		t.Helper()
		simPct = pct
		if err := d.checkDiskWarning(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	unresolved := func() int {
		n := 0
		for _, r := range reports {
			if !r.Resolved {
				n++
			}
		}
		return n
	}
	resolved := func() int {
		n := 0
		for _, r := range reports {
			if r.Resolved {
				n++
			}
		}
		return n
	}

	// Each check above 85% reports the warning; the gateway deduplicates the
	// item, so a repeat raises the entry's count instead of a new item.
	check(86)
	if unresolved() != 1 || !d.diskWarningActive {
		t.Fatalf("at 86%%: %d warnings, active=%v", unresolved(), d.diskWarningActive)
	}
	check(86)
	if unresolved() != 2 {
		t.Fatalf("repeat at 86%%: %d warnings, want 2 reports", unresolved())
	}
	// Between 80% and 85% keeps the warning without resolving it.
	check(82)
	if resolved() != 0 || !d.diskWarningActive {
		t.Fatalf("at 82%%: %d resolves, active=%v", resolved(), d.diskWarningActive)
	}
	// Below 80% resolves it once.
	check(79)
	if resolved() != 1 || d.diskWarningActive {
		t.Fatalf("at 79%%: %d resolves, active=%v", resolved(), d.diskWarningActive)
	}
	check(79)
	if resolved() != 1 {
		t.Fatalf("repeat at 79%% resolved again: %d", resolved())
	}
	// A fresh crossing reports a new warning.
	check(86)
	if unresolved() != 3 {
		t.Fatalf("after re-crossing 85%%: %d warnings, want 3 reports", unresolved())
	}
}

// An agent-net-* temp folder the test scripts or a sandbox left behind is
// removed after six idle hours, while the cache, the Go temp folder and a
// recent folder stay.
func TestWorkerCacheCleanupSweepsOrphanAgentNetTmp(t *testing.T) {
	isolateDaemonCaches(t)
	now := time.Now()
	root := t.TempDir()
	gotmpDir := filepath.Join(root, "agent-net-gotmp")
	cacheDir := filepath.Join(root, "agent-net-gocache")
	orphanRoot := filepath.Join(root, "agent-net-orphan")
	orphan := filepath.Join(orphanRoot, "private")
	recent := filepath.Join(root, "agent-net-recent")
	other := filepath.Join(root, "other-folder")
	for _, dir := range []string{gotmpDir, cacheDir, orphan, recent, other} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeGoCacheMarkers(t, cacheDir)
	for _, dir := range []string{gotmpDir, orphan, recent, other} {
		if err := os.WriteFile(filepath.Join(dir, "file"), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-7 * time.Hour)
	for _, dir := range []string{gotmpDir, orphan, other} {
		if err := os.Chtimes(filepath.Join(dir, "file"), old, old); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(orphanRoot, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cacheDir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(cacheDir, "README"), old, old); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		cfg: Config{WorkerGoCache: cacheDir, WorkerGoTmp: gotmpDir, testTrimRoot: root},
	}
	freed := d.cleanWorkerGoTmp(now, 0)
	if freed == 0 {
		t.Fatal("the orphan sweep freed nothing")
	}
	if _, err := os.Stat(filepath.Join(root, "agent-net-orphan")); !os.IsNotExist(err) {
		t.Errorf("orphan folder idle for seven hours was kept: %v", err)
	}
	for _, kept := range []string{gotmpDir, cacheDir, recent, other} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept: %v", kept, err)
		}
	}
}

func TestWorkerCacheTestDaemonIsolation(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})

	// A test's folders sit under the system temp folder, or under GOTMPDIR, which
	// go test uses for t.TempDir and which is the RAM folder when a test script runs.
	tempRoots := []string{os.TempDir(), filepath.Dir(filepath.Dir(t.TempDir()))}
	inTemp := func(path string) bool {
		for _, root := range tempRoots {
			if strings.HasPrefix(path, root) {
				return true
			}
		}
		return false
	}

	cacheDir := n.d.workerGoCacheDir()
	tmpDir := n.d.workerGoTmpDir()

	if cacheDir == "" || cacheDir == "/tmp/agent-net-gocache" || !inTemp(cacheDir) {
		t.Fatalf("default test daemon workerGoCacheDir = %q, want inside a test temp dir", cacheDir)
	}
	if tmpDir == "" || tmpDir == "/tmp/agent-net-gotmp" {
		t.Fatalf("default test daemon workerGoTmpDir = %q, want inside test temp dir", tmpDir)
	}

	if !testTempFolder(cacheDir) {
		t.Fatalf("workerGoCacheDir %q is not inside a test temp folder", cacheDir)
	}
	if !testTempFolder(tmpDir) {
		t.Fatalf("workerGoTmpDir %q is not inside a test temp folder", tmpDir)
	}
	ownerCache := ownerGoBuildCacheDir(n.d.cfg.HomeDir)
	if ownerCache == "" || !testTempFolder(ownerCache) {
		t.Fatalf("owner Go cache %q is not inside a test temp folder", ownerCache)
	}

	w := startWorkerNode(t, g, shortSet, nil)
	wCache := w.d.workerGoCacheDir()
	wTmp := w.d.workerGoTmpDir()
	if wCache == "" || wCache == "/tmp/agent-net-gocache" || !testTempFolder(wCache) {
		t.Fatalf("default test workerNode workerGoCacheDir = %q, want inside temp dir", wCache)
	}
	if wTmp == "" || wTmp == "/tmp/agent-net-gotmp" || !testTempFolder(wTmp) {
		t.Fatalf("default test workerNode workerGoTmpDir = %q, want inside temp dir", wTmp)
	}

	// A daemon built from a bare Config (no WorkerGoCache / WorkerGoTmp) falls back
	// to the package-wide guard configured in TestMain (AGENT_NET_GOCACHE and AGENT_NET_GOTMPDIR).
	bareDaemon, err := New(Config{StateDir: t.TempDir(), SocketPath: filepath.Join(shortDir(t), "d.sock")})
	if err != nil {
		t.Fatal(err)
	}
	bareCache := bareDaemon.workerGoCacheDir()
	bareTmp := bareDaemon.workerGoTmpDir()
	if bareCache == "" || bareCache == "/tmp/agent-net-gocache" {
		t.Fatalf("bare daemon workerGoCacheDir = %q, want guarded temp dir", bareCache)
	}
	if bareTmp == "" || bareTmp == "/tmp/agent-net-gotmp" {
		t.Fatalf("bare daemon workerGoTmpDir = %q, want guarded temp dir", bareTmp)
	}
	if !testTempFolder(bareCache) {
		t.Fatalf("bare daemon workerGoCacheDir %q is not inside a test temp folder", bareCache)
	}
	if !testTempFolder(bareTmp) {
		t.Fatalf("bare daemon workerGoTmpDir %q is not inside a test temp folder", bareTmp)
	}
}
