package daemon

import (
	"context"
	"time"
)

// The delivered table is a duplicate check, not message history. A row is
// kept until its message expires and a week after that, so a replay of a live
// message is still refused. The first prune runs soon after start because the
// daemon restarts on every upgrade.
var (
	deliveredPruneFirstDelay = time.Minute
	deliveredPruneEvery      = time.Hour
)

func (d *Daemon) deliveredPruneLoop(ctx context.Context) {
	runEvery(ctx, deliveredPruneFirstDelay, deliveredPruneEvery, func() {
		if _, err := d.st.pruneDelivered(time.Now()); err != nil && d.log != nil {
			d.Logf("housekeeping delivered error=%v", err)
		}
	})
}
