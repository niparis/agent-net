package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// operatorReadCache shares expensive owner-only reads and keeps successful
// results briefly. Each waiter can leave without cancelling the shared read.
type operatorReadCache struct {
	mu      sync.Mutex
	entries map[string]*operatorRead
}
type operatorRead struct {
	done    chan struct{}
	value   any
	err     error
	expires time.Time
}

func (c *operatorReadCache) do(ctx context.Context, method string, raw json.RawMessage, fn func(context.Context) (any, error)) (any, error) {
	key := method + "\x00" + string(raw)
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]*operatorRead)
	}
	now := time.Now()
	for k, entry := range c.entries {
		if !entry.expires.IsZero() && !now.Before(entry.expires) {
			delete(c.entries, k)
		}
	}
	e := c.entries[key]
	if e == nil {
		e = &operatorRead{done: make(chan struct{})}
		c.entries[key] = e
		go func() {
			callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
			defer cancel()
			value, err := fn(callCtx)
			c.mu.Lock()
			e.value, e.err = value, err
			if err == nil {
				e.expires = time.Now().Add(time.Second)
			} else {
				delete(c.entries, key)
			}
			close(e.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return e.value, e.err
	}
}
