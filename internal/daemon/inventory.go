package daemon

import (
	"context"
	"os"
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
// HarnessInterval, retrying empty model probes sooner, and tells the gateway
// after each run. Detection can take
// seconds (it runs `opencode models`), so it never blocks start-up: hello
// carries the inventory when it is ready, and an update follows when it is not.
func (d *Daemon) harnessLoop(ctx context.Context) {
	retry := d.cfg.HarnessRetryDelay
	if retry <= 0 {
		retry = time.Minute
	}
	nextRetry := retry
	for {
		_, incomplete := d.refreshHarnessInventory(ctx)
		if ctx.Err() != nil {
			return
		}
		delay := d.cfg.HarnessInterval
		if incomplete {
			delay = min(nextRetry, d.cfg.HarnessInterval)
			if nextRetry < 30*time.Minute {
				nextRetry *= 2
				if nextRetry > 30*time.Minute {
					nextRetry = 30 * time.Minute
				}
			}
		} else {
			nextRetry = retry
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (d *Daemon) refreshHarnessInventory(ctx context.Context) ([]protocol.HarnessInfo, bool) {
	d.hdetectMu.Lock()
	defer d.hdetectMu.Unlock()
	list := d.cfg.DetectHarnesses(ctx)
	if ctx.Err() != nil {
		return nil, false
	}
	if list == nil {
		list = []protocol.HarnessInfo{}
	}
	incomplete := false
	for _, h := range list {
		if h.Path != "" && modelsProbed(h.Name) && len(h.Models) == 0 {
			incomplete = true
		}
	}
	d.hmu.Lock()
	list = keepMissingHarnesses(d.installed, list, d.Logf)
	for i := range list {
		list[i].ModelProviders = d.cfg.ModelProviders
		if list[i].Name == "claude" {
			list[i].PolicyReady = policyHookInstalled()
		}
	}
	d.installed = list
	d.hgen++
	d.hmu.Unlock()
	var names []string
	for _, h := range list {
		names = append(names, h.Name)
	}
	d.Logf("harnesses: %s", strings.Join(names, ","))
	d.publishHarnesses(ctx)
	return list, incomplete
}

func modelsProbed(name string) bool { return name == "opencode" || name == "pi" || name == "agy" }

// keepMissingHarnesses keeps a harness that was found before when a detection
// run does not report it but its binary is still there. A probe can fail or
// time out under load, and dropping the entry then makes a start say the
// harness is not installed. Only a missing binary removes it.
func keepMissingHarnesses(prev, next []protocol.HarnessInfo, logf func(string, ...any)) []protocol.HarnessInfo {
	seen := make(map[string]bool, len(next))
	known := make(map[string]bool, len(prev))
	previous := make(map[string]protocol.HarnessInfo, len(prev))
	for _, h := range prev {
		previous[h.Name] = h
		if h.Path != "" {
			known[h.Name] = true
		}
	}
	out := make([]protocol.HarnessInfo, 0, len(next)+len(prev))
	for _, h := range next {
		if old := previous[h.Name]; h.Update == nil {
			h.Update = old.Update
		}
		if old := previous[h.Name]; h.SignedIn == "" {
			h.SignedIn, h.SignIn = old.SignedIn, old.SignIn
		}
		// An entry a failed probe could not fill in gives way to the copy
		// found before. Without an earlier copy it is all there is.
		if h.Path == "" && known[h.Name] {
			continue
		}
		if old := previous[h.Name]; h.Path != "" && modelsProbed(h.Name) && len(h.Models) == 0 && len(old.Models) > 0 {
			h.Models = old.Models
			if logf != nil {
				logf("harnesses: %s model probe failed; keeping the previous models", h.Name)
			}
		}
		out = append(out, h)
		seen[h.Name] = true
	}
	for _, h := range prev {
		if seen[h.Name] {
			continue
		}
		if h.Path != "" {
			if _, err := os.Stat(h.Path); err == nil {
				if logf != nil {
					logf("harnesses: %s probe failed; keeping the previous copy at %s", h.Name, h.Path)
				}
				out = append(out, h)
				seen[h.Name] = true
				continue
			}
		}
		if logf != nil {
			logf("harnesses: %s is gone (%s)", h.Name, h.Path)
		}
	}
	return out
}

// publishHarnesses sends the current inventory if the link is up. If it is
// not, the next hello carries it.
func (d *Daemon) publishHarnesses(ctx context.Context) {
	list, _ := d.inventory()
	if list == nil {
		return
	}
	if _, err := d.call(ctx, "node.harnesses", protocol.NodeHarnesses{Harnesses: list, Launch: d.launchFlag()}); err != nil &&
		err != errNotConnected && ctx.Err() == nil && !strings.Contains(err.Error(), "unknown frame type") {
		// An older gateway does not know the request. That is fine.
		d.Logf("node.harnesses: %v", err)
	}
}
