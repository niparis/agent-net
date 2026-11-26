package daemon

import (
	"context"
	"errors"
	"time"
)

// errHandbackDeferred ends a hand-back that waits for the gateway link. The
// harness keeps the turn itself, so the next adoption hands it back again.
var errHandbackDeferred = errors.New("the hand-back waits for the gateway link")

// harnessKeepsHandback reports whether the harness's own state carries a
// hand-back across a daemon restart, so it is never saved in the state
// database: the agy stream state (worker_agy_stream.go), and the Codex thread,
// whose last turn adoption hands back again while nothing marks it handled.
// While the link is down these hand-backs wait in handbackFlow instead.
func harnessKeepsHandback(harness string) bool {
	return harness == "agy" || harness == "codex"
}

// waitForLink returns once the gateway link is up, or false when ctx ends
// first. Adoption runs before Start launches the link.
func (d *Daemon) waitForLink(ctx context.Context) bool {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !d.Connected() {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
	return true
}
