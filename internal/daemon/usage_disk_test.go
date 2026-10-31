package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/usage"
)

func TestUsageDiskFreeIsCachedForOneMinute(t *testing.T) {
	var u usageState
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	reads := 0
	read := func() uint64 {
		reads++
		return uint64(reads) << 30
	}
	if got := u.diskFreeAt(now, read); got != 1<<30 {
		t.Fatalf("initial disk free = %d", got)
	}
	if got := u.diskFreeAt(now.Add(59*time.Second), read); got != 1<<30 {
		t.Fatalf("cached disk free = %d", got)
	}
	if reads != 1 {
		t.Fatalf("read disk %d times before a minute", reads)
	}
	if got := u.diskFreeAt(now.Add(time.Minute), read); got != 2<<30 {
		t.Fatalf("refreshed disk free = %d", got)
	}
	if reads != 2 {
		t.Fatalf("read disk %d times after refresh", reads)
	}
}

func TestHomeDiskFreeUsesConfiguredTestHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := homeDiskFree(); got == 0 {
		t.Fatal("no free space reported for the temporary home filesystem")
	}
}

func TestMachineUsageAddsCachedDiskFree(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	sampler := liveSampler(500)
	sampler.Sample(context.Background())
	d := &Daemon{cfg: Config{UsageSampler: sampler}, st: st}
	d.usage.diskFreeAt(time.Now(), func() uint64 { return 31 << 30 })
	rep, ok := d.usageReport()
	if !ok {
		t.Fatal("usage report unavailable")
	}
	if rep.Machine.DiskFree != 31<<30 {
		t.Fatalf("reported disk free = %d", rep.Machine.DiskFree)
	}
	if rep.Machine.Cores != 4 {
		t.Fatalf("machine core count = %d", rep.Machine.Cores)
	}
}

var _ usage.Reader = (*liveReader)(nil)
