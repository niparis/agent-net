package daemon

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/niparis/agent-net/internal/upgrade"
)

// defaultAutoUpgradeDelay bounds the random wait before an automatic upgrade,
// so machines that reconnect together do not all download at once.
const defaultAutoUpgradeDelay = 10 * time.Second

// autoUpgrade runs after each handshake. If the gateway serves a newer release
// than this daemon runs, it installs it through the same path as the gateway's
// node.upgrade request and restarts. Failures are remembered in the state dir
// so the same version is not tried again within upgrade.RetryAfter.
func (d *Daemon) autoUpgrade(ctx context.Context) {
	if d.cfg.NoAutoUpgrade || d.helloVersion() == "dev" {
		return
	}
	max := d.cfg.AutoUpgradeMaxDelay
	if max == 0 {
		max = defaultAutoUpgradeDelay
	}
	if max > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(rand.Int63n(int64(max) + 1))):
		}
	}
	served, err := d.servedVersion(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.Logf("auto-upgrade: cannot read the version the gateway serves: %v", err)
		}
		return
	}
	cur := d.helloVersion()
	dec := upgrade.DecideAuto(cur, served, false, upgrade.LoadAuto(d.cfg.StateDir), time.Now())
	if !dec.Upgrade {
		if upgrade.IsNewer(served, cur) { // a newer release that is skipped is worth a line
			d.Logf("auto-upgrade: not upgrading %s to %s: %s", cur, served, dec.Reason)
		}
		return
	}
	if dec.ClearHold {
		if err := upgrade.ClearHold(d.cfg.StateDir); err != nil {
			d.Logf("auto-upgrade: cannot clear the rollback hold: %v", err)
		}
	}
	d.Logf("auto-upgrade: the gateway serves %s and this machine runs %s, upgrading", served, cur)
	res, joined, err := d.upgradeTo(ctx, served)
	if err != nil {
		if errors.Is(err, errUpgradeBusy) || ctx.Err() != nil {
			return
		}
		if e := upgrade.RecordFailure(d.cfg.StateDir, served, time.Now(), err); e != nil {
			d.Logf("auto-upgrade: cannot remember the failure: %v", e)
		}
		d.Logf("auto-upgrade to %s failed, staying on %s, not trying %s again for %s: %v", served, cur, served, upgrade.RetryAfter, err)
		return
	}
	d.Logf("auto-upgrade: %s -> %s installed, restarting: %v", cur, res.To, res.Restarting)
	if res.Restarting && !joined {
		time.Sleep(300 * time.Millisecond) // let an answer to the gateway leave
		d.restart()
	}
}

// servedVersion is the version the gateway serves to this machine.
func (d *Daemon) servedVersion(ctx context.Context) (string, error) {
	if d.cfg.Served != nil {
		return d.cfg.Served(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return upgrade.ServedVersion(ctx, d.client, d.cfg.GatewayURL)
}
