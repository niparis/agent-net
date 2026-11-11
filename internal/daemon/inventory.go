package daemon

import (
	"context"
	"os"
	"strings"

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

// harnessLoop detects the installed harnesses once at start and tells the
// gateway. There is no periodic model scan: a model list is read from the
// harness live when someone asks for one. Detection still runs when a harness
// update check needs it (harness_update.go).
func (d *Daemon) harnessLoop(ctx context.Context) {
	d.refreshHarnessInventory(ctx)
}

func (d *Daemon) refreshHarnessInventory(ctx context.Context) []protocol.HarnessInfo {
	d.hdetectMu.Lock()
	defer d.hdetectMu.Unlock()
	list := d.cfg.DetectHarnesses(ctx)
	if ctx.Err() != nil {
		return nil
	}
	if list == nil {
		list = []protocol.HarnessInfo{}
	}
	d.hmu.Lock()
	list = keepMissingHarnesses(d.installed, list, d.Logf)
	for i := range list {
		list[i].ModelProviders = d.cfg.ModelProviders
		if list[i].Name == "claude" {
			list[i].PolicyReady = policyHookInstalled()
		}
	}
	d.hmu.Unlock()
	d.checkClaudeDelivery(list)
	d.checkOpenCodeCapabilities(ctx, list)
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
	return list
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
	if _, err := d.call(ctx, "node.harnesses", protocol.NodeHarnesses{Harnesses: list, Launch: d.launchFlag(), Health: d.machineHealth()}); err != nil &&
		err != errNotConnected && ctx.Err() == nil && !strings.Contains(err.Error(), "unknown frame type") {
		// An older gateway does not know the request. That is fine.
		d.Logf("node.harnesses: %v", err)
	}
}
