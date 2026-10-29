package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// inventory returns the installed harnesses found so far and a counter that
// grows with each new inventory. The list is nil until the first detection ends.
func (d *Daemon) inventory() ([]protocol.HarnessInfo, int) {
	d.hmu.Lock()
	defer d.hmu.Unlock()
	if d.installed == nil {
		return nil, d.hgen
	}
	return append([]protocol.HarnessInfo{}, d.installed...), d.hgen
}

// harnessLoop detects the installed harnesses at start and then every
// HarnessInterval, and tells the gateway after each run. Detection can take
// seconds (it runs `opencode models`), so it never blocks start-up: hello
// carries the inventory when it is ready, and an update follows when it is not.
func (d *Daemon) harnessLoop(ctx context.Context) {
	t := time.NewTicker(d.cfg.HarnessInterval)
	defer t.Stop()
	for {
		list := d.cfg.DetectHarnesses(ctx)
		if ctx.Err() != nil {
			return
		}
		if list == nil {
			list = []protocol.HarnessInfo{} // "none installed", not "not reported"
		}
		d.hmu.Lock()
		d.installed = list
		d.hgen++
		d.hmu.Unlock()
		var names []string
		for _, h := range list {
			names = append(names, h.Name)
		}
		d.Logf("harnesses: %s", strings.Join(names, ","))
		d.publishHarnesses(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// publishHarnesses sends the current inventory if the link is up. If it is
// not, the next hello carries it.
func (d *Daemon) publishHarnesses(ctx context.Context) {
	list, _ := d.inventory()
	if list == nil {
		return
	}
	if _, err := d.call(ctx, "node.harnesses", protocol.NodeHarnesses{Harnesses: list}); err != nil &&
		err != errNotConnected && ctx.Err() == nil && !strings.Contains(err.Error(), "unknown frame type") {
		// An older gateway does not know the request. That is fine.
		d.Logf("node.harnesses: %v", err)
	}
}
