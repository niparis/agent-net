package daemon

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's setup integrations (decision 0021). The gateway keeps one answer
// per item for the whole network. This daemon applies them at start and checks
// again every minute, so a save in the TUI reaches every machine without a
// setup run. A machine's local answer wins.

const defaultIntegrationsInterval = time.Minute

func (d *Daemon) integrationsClient() (integrations.Client, bool) {
	if d.cfg.GatewayURL == "" || d.cfg.Token == "" {
		return integrations.Client{}, false
	}
	// d.client already trusts only the gateway CA. Building a new pinned
	// client here on every call leaked one idle connection per call.
	return integrations.Client{URL: d.cfg.GatewayURL, Token: d.cfg.Token, HTTP: d.client}, true
}

func (d *Daemon) integrationsLoop(ctx context.Context) {
	interval := d.cfg.IntegrationsInterval
	if interval <= 0 {
		interval = defaultIntegrationsInterval
	}
	d.applyNetworkIntegrations(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.intgWake:
		}
		d.applyNetworkIntegrations(ctx)
	}
}

// applyNetworkIntegrations reads the answers and applies them when they differ
// from the last ones applied. A gateway that is not reachable yet is tried
// again at the next tick.
func (d *Daemon) applyNetworkIntegrations(ctx context.Context) {
	c, ok := d.integrationsClient()
	if !ok {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.Get(cctx)
	if err != nil || !res.Set {
		return
	}
	d.intgMu.Lock()
	same := d.intgSet && d.intgLast == res.Integrations
	d.intgMu.Unlock()
	if same {
		return
	}
	if err := d.cfg.ApplyIntegrations(res.Integrations, d.Logf); err != nil {
		d.Logf("integrations: apply failed: %v", err)
		return
	}
	d.intgMu.Lock()
	d.intgLast, d.intgSet = res.Integrations, true
	d.intgMu.Unlock()
}

func (d *Daemon) wakeIntegrations() {
	select {
	case d.intgWake <- struct{}{}:
	default:
	}
}

// integrationsGet is the owner-only operator method. The gate is in
// dispatchOperator.
func (d *Daemon) integrationsGet(ctx context.Context) (protocol.IntegrationsResult, error) {
	c, ok := d.integrationsClient()
	if !ok {
		return protocol.IntegrationsResult{}, errNotConnected
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.Get(cctx)
}

// integrationsSet stores the answers for every machine and applies them here
// at once. Other machines check at their next tick.
func (d *Daemon) integrationsSet(ctx context.Context, in protocol.Integrations) (protocol.IntegrationsResult, error) {
	c, ok := d.integrationsClient()
	if !ok {
		return protocol.IntegrationsResult{}, errNotConnected
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.Set(cctx, in)
	if err != nil {
		return res, err
	}
	res.Set = true
	d.wakeIntegrations()
	return res, nil
}
