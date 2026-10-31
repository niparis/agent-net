package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOperatorReadCoalescesAndCaches(t *testing.T) {
	var c operatorReadCache
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func(context.Context) (any, error) { calls.Add(1); close(started); <-release; return "ok", nil }
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			v, err := c.do(context.Background(), "workers", []byte(`{}`), fn)
			if err != nil || v != "ok" {
				t.Errorf("got %v, %v", v, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("underlying calls = %d, want 1", got)
	}
	v, err := c.do(context.Background(), "workers", []byte(`{}`), fn)
	if err != nil || v != "ok" || calls.Load() != 1 {
		t.Fatalf("cached: %v, %v, calls %d", v, err, calls.Load())
	}
}

func TestOperatorReadWaiterCancellation(t *testing.T) {
	var c operatorReadCache
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.do(context.Background(), "workers", nil, func(context.Context) (any, error) { close(started); <-release; return 1, nil })
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.do(ctx, "workers", nil, nil)
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("call did not finish")
	}
}

func TestOperatorReadDoesNotCacheErrorsOrOtherParams(t *testing.T) {
	var c operatorReadCache
	var calls int
	fail := func(context.Context) (any, error) { calls++; return nil, context.DeadlineExceeded }
	for i := 0; i < 2; i++ {
		if _, err := c.do(context.Background(), "usage_top", []byte(`{"session_id":"a"}`), fail); err == nil {
			t.Fatal("expected error")
		}
	}
	ok := func(context.Context) (any, error) { calls++; return calls, nil }
	a, _ := c.do(context.Background(), "usage_top", []byte(`{"session_id":"a"}`), ok)
	b, _ := c.do(context.Background(), "usage_top", []byte(`{"session_id":"b"}`), ok)
	if calls != 4 || a == b {
		t.Fatalf("calls %d, results %v and %v", calls, a, b)
	}
}
