// Package daemon is a fixture for the lock check. Its import path ends in
// internal/daemon, so the check applies to it.
package daemon

import (
	"context"
	"net/http"
	"os/exec"
	"sync"
)

type Daemon struct {
	mu                sync.Mutex
	diskWarningActive bool
	client            *http.Client
}

type link struct{ mu sync.RWMutex }

func (d *Daemon) call(ctx context.Context, typ string) error  { return nil }
func (d *Daemon) send(ctx context.Context, text string) error { return nil }
func (l *link) call(ctx context.Context, typ string) error    { return nil }

// checkDiskWarningBefore has the shape checkDiskWarning had before commit
// 096d069f81 ("Fix disk warning deadlock in daemon"). The defer holds d.mu while
// the gateway call runs. A gateway answer that needs d.mu deadlocks there.
func (d *Daemon) checkDiskWarningBefore(ctx context.Context, pct float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if pct < 85 {
		return nil
	}
	if d.diskWarningActive {
		return nil
	}
	if err := d.call(ctx, "inbox.create"); err != nil { // want `call to d.call while d.mu is held`
		return err
	}
	d.diskWarningActive = true
	return nil
}

// A deferred Unlock holds the lock to the return, so a send after a branch is
// still under it.
func (d *Daemon) flaggedSendAfterBranch(ctx context.Context, x bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if x {
		return nil
	}
	return d.send(ctx, "later") // want `call to d.send while d.mu is held`
}

func (d *Daemon) flaggedExec(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cmd := exec.CommandContext(ctx, "git", "status") // want `call to exec.CommandContext while d.mu is held`
	return cmd.Run()                                 // want `call to cmd.Run while d.mu is held`
}

func (d *Daemon) flaggedLinkCall(ctx context.Context, l *link) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.call(ctx, "x") // want `call to l.call while l.mu is held`
}

func (d *Daemon) flaggedHTTP() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if resp, err := d.client.Get("http://example.invalid/"); err == nil { // want `call to d.client.Get while d.mu is held`
		resp.Body.Close()
	}
}

// A lock taken in one branch is still held after the branch.
func (d *Daemon) flaggedLockInBranch(ctx context.Context, x bool) error {
	if x {
		d.mu.Lock()
	}
	return d.call(ctx, "maybe") // want `call to d.call while d.mu is held`
}

// The lock is still held when the loop comes round again.
func (d *Daemon) flaggedInLoop(ctx context.Context, items []string) error {
	for range items {
		d.mu.Lock()
		if err := d.call(ctx, "row"); err != nil { // want `call to d.call while d.mu is held`
			return err
		}
		d.mu.Unlock()
	}
	return nil
}

// The state is read under the lock, and the call runs after the unlock.
func (d *Daemon) cleanReadThenCall(ctx context.Context) error {
	d.mu.Lock()
	active := d.diskWarningActive
	d.mu.Unlock()
	if active {
		return nil
	}
	return d.call(ctx, "inbox.create")
}

// Each branch unlocks before its call.
func (d *Daemon) cleanBranches(ctx context.Context, x bool) error {
	d.mu.Lock()
	if x {
		d.mu.Unlock()
		return d.call(ctx, "a")
	}
	d.mu.Unlock()
	return d.call(ctx, "b")
}

// A goroutine started under the lock runs without it.
func (d *Daemon) cleanGoroutine(ctx context.Context) {
	d.mu.Lock()
	go d.call(ctx, "background")
	d.mu.Unlock()
}

// A function literal does not run under the lock of the code that creates it.
func (d *Daemon) cleanLiteral(ctx context.Context) func() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return func() error { return d.call(ctx, "later") }
}

// A break leaves the loop with the lock released.
func (d *Daemon) cleanBreak(ctx context.Context) error {
	for {
		d.mu.Lock()
		if d.diskWarningActive {
			d.mu.Unlock()
			break
		}
		d.mu.Unlock()
		return nil
	}
	return d.call(ctx, "after")
}

// The call comes before the lock, so nothing is held when it runs.
func (d *Daemon) cleanCallBeforeLock(ctx context.Context) error {
	if err := d.call(ctx, "first"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return nil
}
