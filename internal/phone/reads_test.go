package phone

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type blockingReadSource struct {
	*consoleOperator
	calls   atomic.Int32
	fail    atomic.Bool
	release chan struct{}
}

func (f *blockingReadSource) Call(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
	f.calls.Add(1)
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if f.fail.Swap(false) {
		return nil, errors.New("temporary read failure")
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func TestConcurrentConsoleReadsShareOneCall(t *testing.T) {
	f := &blockingReadSource{release: make(chan struct{})}
	a := &app{src: f}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := a.read(context.Background(), "fixture", nil)
			if err != nil || string(raw) != `{"ok":true}` {
				t.Errorf("read=%s error=%v", raw, err)
			}
		}()
	}
	close(f.release)
	wg.Wait()
	if f.calls.Load() != 1 {
		t.Fatalf("source calls=%d want=1", f.calls.Load())
	}
}
func TestConsoleReadFailuresAreRetried(t *testing.T) {
	f := &blockingReadSource{release: make(chan struct{})}
	close(f.release)
	f.fail.Store(true)
	a := &app{src: f}
	if _, err := a.read(context.Background(), "fixture", nil); err == nil {
		t.Fatal("first read should fail")
	}
	if _, err := a.read(context.Background(), "fixture", nil); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("source calls=%d want=2", f.calls.Load())
	}
}
