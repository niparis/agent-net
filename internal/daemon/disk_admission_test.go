package daemon

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestAdmitDisk(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), DiskFree: func(string) (uint64, error) { return 6 << 30, nil }}}
	got := d.diskAdmission()
	if got.OK || got.Room != protocol.RoomDisk || got.Reason != "disk: 6 GB free (floor 15 GB)" {
		t.Fatalf("low disk: %+v", got)
	}
	d.cfg.DiskFree = func(string) (uint64, error) { return 16 << 30, nil }
	got = d.diskAdmission()
	if !got.OK {
		t.Fatalf("space recovered: %+v", got)
	}
}

func TestAdmitDiskCacheFilesystem(t *testing.T) {
	state := t.TempDir()
	cache := t.TempDir()
	d := &Daemon{cfg: Config{StateDir: state, WorkerCacheDirs: []string{cache}, DiskFree: func(path string) (uint64, error) {
		if path == cache {
			return 5 << 30, nil
		}
		return 20 << 30, nil
	}}}
	if got := d.diskAdmission(); got.OK || got.Room != protocol.RoomDisk || got.Reason != "disk: 5 GB free (floor 15 GB)" {
		t.Fatalf("cache disk: %+v", got)
	}
}

func TestQueueDiskStartsAfterSpaceReturns(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var free atomic.Uint64
	free.Store(6 << 30)
	root := t.TempDir()
	w := startWorkerNode(t, g, queueSet, func(c *Config) {
		c.LoadProbe = func() MachineLoad { return roomyLoad() }
		c.DiskFree = func(string) (uint64, error) { return free.Load(), nil }
		c.DiskRoot = func(string) string { return root }
		c.DiskNotice = func(context.Context, string) error { return nil }
	})
	repo := newRepo(t, "diskqueue")
	w.register(t, repo)
	pi, _ := helloPi(t, w.node, "caller", "/work/caller", true)
	result := startFrom(t, pi, protocol.WorkerStartRequest{Project: "diskqueue", Harness: "opencode", Task: "test"})
	if result.Status != "queued" || !strings.Contains(result.QueueReason, "disk: 6 GB free") {
		t.Fatalf("queued: %+v", result)
	}
	free.Store(20 << 30)
	eventually(t, "disk queued worker started", func() bool { return len(w.logs.lines("worker.dequeued")) == 1 })
}

func TestDiskFloorEnvironmentOverride(t *testing.T) {
	t.Setenv("AGENT_NET_MIN_FREE_GB", "22")
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), Workers: WorkerSettings{MinFreeGB: 10}, DiskFree: func(string) (uint64, error) { return 20 << 30, nil }}}
	got := d.diskAdmission()
	if got.OK || got.Reason != "disk: 20 GB free (floor 22 GB)" {
		t.Fatalf("override: %+v", got)
	}
}
