package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerCacheCleanupAgeRules(t *testing.T) {
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

	freed := d.trimWorkerGoCache(now, 24*time.Hour)
	if freed != 0 {
		t.Fatalf("expected 0 freed from owner cache, got %d", freed)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim file in owner cache was deleted: %v", err)
	}
}

func TestWorkerCacheCleanupOffSwitch(t *testing.T) {
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

func TestDiskWarningHysteresis(t *testing.T) {
	var createdItems []map[string]any
	var answeredItems []map[string]string
	var simPct float64

	d := &Daemon{
		cfg: Config{
			NodeID:   "n_test",
			Hostname: "test-node",
			StateDir: t.TempDir(),
			testGatewayCall: func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
				switch typ {
				case "inbox.create":
					p := params.(map[string]any)
					createdItems = append(createdItems, p)
					item := protocol.InboxItem{
						ID:             "i_disk_warning_1",
						Kind:           p["kind"].(string),
						SessionID:      p["session_id"].(string),
						Text:           p["text"].(string),
						Recommendation: p["recommendation"].(string),
						Status:         "open",
					}
					raw, _ := json.Marshal(map[string]any{"item": item})
					return raw, true, nil
				case "inbox.answer":
					p := params.(map[string]string)
					answeredItems = append(answeredItems, p)
					item := protocol.InboxItem{
						ID:     p["item_id"],
						Status: "dismissed",
					}
					raw, _ := json.Marshal(map[string]any{"item": item})
					return raw, true, nil
				case "inbox.list":
					raw, _ := json.Marshal(map[string]any{"items": []protocol.InboxItem{}})
					return raw, true, nil
				}
				return nil, false, nil
			},
			testDiskPercent: func(path string) (float64, bool) {
				return simPct, true
			},
		},
	}

	now := time.Now()

	// 1. At 86% (> 85%): raises one inbox item.
	simPct = 86.0
	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(createdItems) != 1 {
		t.Fatalf("expected 1 created item at 86%%, got %d", len(createdItems))
	}
	if !d.diskWarningActive {
		t.Errorf("expected diskWarningActive = true")
	}

	// 2. At 86% again: no repeat spam!
	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(createdItems) != 1 {
		t.Fatalf("expected still 1 created item (no repeat spam), got %d", len(createdItems))
	}

	// 3. At 82%: between 80% and 85%, hysteresis keeps warning active, does not clear.
	simPct = 82.0
	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(answeredItems) != 0 {
		t.Fatalf("expected 0 answered/cleared at 82%%, got %d", len(answeredItems))
	}
	if !d.diskWarningActive {
		t.Errorf("expected warning to stay active at 82%%")
	}

	// 4. Drops below 80% (79%): clears warning!
	simPct = 79.0
	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(answeredItems) != 1 {
		t.Fatalf("expected 1 answered/cleared item at 79%%, got %d", len(answeredItems))
	}
	if answeredItems[0]["answer"] != "dismiss" {
		t.Errorf("expected answer dismiss, got %s", answeredItems[0]["answer"])
	}
	if d.diskWarningActive {
		t.Errorf("expected diskWarningActive = false after drop below 80%%")
	}

	// 5. At 79% again: no repeated clear.
	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(answeredItems) != 1 {
		t.Fatalf("expected still 1 answered item, got %d", len(answeredItems))
	}

	// 6. Rises again to 86%: raises warning again.
	simPct = 86.0
	if err := d.checkDiskWarning(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(createdItems) != 2 {
		t.Fatalf("expected 2 created items after re-crossing 85%%, got %d", len(createdItems))
	}
}
