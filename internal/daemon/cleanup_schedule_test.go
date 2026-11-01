package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The first cleanup must not wait for the long interval: the daemon restarts
// on every upgrade, which would reset a plain ticker before it fires.
func TestCleanupRunsSoonAfterStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	go runEvery(ctx, 10*time.Millisecond, time.Hour, func() { runs.Add(1) })
	eventually(t, "first cleanup", func() bool { return runs.Load() == 1 })
}

func TestCleanupRepeats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	go runEvery(ctx, time.Millisecond, 5*time.Millisecond, func() { runs.Add(1) })
	eventually(t, "repeated cleanups", func() bool { return runs.Load() >= 3 })
}
