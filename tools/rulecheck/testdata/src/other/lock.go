// Package other is outside internal/daemon and internal/gateway, so the lock
// check does not apply to it, even with the same shape.
package other

import (
	"context"
	"sync"
)

type Daemon struct{ mu sync.Mutex }

func (d *Daemon) call(ctx context.Context) error { return nil }

func (d *Daemon) notChecked(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.call(ctx)
}
