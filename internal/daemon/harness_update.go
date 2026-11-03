package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/integrations"
	"github.com/niparis/agent-net/internal/protocol"
)

func updateDelay(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func (d *Daemon) harnessUpdatesAllowed() bool {
	d.intgMu.Lock()
	known, off := d.updateSettingKnown, d.updateSettingOff
	d.intgMu.Unlock()
	if known {
		return !off
	}
	applied, ok := integrations.LoadApplied(ConfigDir())
	return !ok || !applied.HarnessUpdatesOff
}

func (d *Daemon) harnessUpdateLoop(ctx context.Context) {
	initial := updateDelay(d.cfg.HarnessUpdateInitialDelay, 10*time.Minute)
	daily := updateDelay(d.cfg.HarnessUpdateInterval, 24*time.Hour)
	retry := updateDelay(d.cfg.HarnessUpdateRetryDelay, 30*time.Minute)
	if !waitHarnessUpdate(ctx, initial) {
		return
	}
	for ctx.Err() == nil {
		nextDay := time.Now().Add(daily)
		if d.harnessUpdatesAllowed() {
			d.refreshHarnessInventory(ctx)
			d.refreshUpdateOnly(ctx)
			hs, _ := d.inventory()
			pending := make(map[string]bool)
			for _, h := range hs {
				if h.Path != "" && updateArgs(h.Name) != nil {
					pending[h.Name] = true
				}
			}
			for len(pending) > 0 && ctx.Err() == nil && d.harnessUpdatesAllowed() {
				for _, h := range hs {
					if !pending[h.Name] || !d.harnessUpdatesAllowed() || ctx.Err() != nil {
						continue
					}
					if d.harnessBusy(h.Name) {
						d.setHarnessUpdate(ctx, h.Name, &protocol.HarnessUpdate{CheckedAt: time.Now().UTC(), Before: h.Version, After: h.Version, Result: "skipped busy"})
						continue
					}
					delete(pending, h.Name)
					d.updateHarness(ctx, h)
				}
				if len(pending) == 0 || !d.harnessUpdatesAllowed() {
					break
				}
				remaining := time.Until(nextDay)
				if remaining <= 0 || !waitHarnessUpdate(ctx, min(retry, remaining)) {
					break
				}
				if !time.Now().Before(nextDay) {
					break
				}
				hs, _ = d.inventory()
			}
		}
		if delay := time.Until(nextDay); delay > 0 && !waitHarnessUpdate(ctx, delay) {
			return
		}
	}
}

func waitHarnessUpdate(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func updateArgs(name string) []string {
	switch name {
	case "claude", "codex", "agy":
		return []string{"update"}
	case "opencode":
		return []string{"upgrade"}
	case "pi":
		return []string{"update", "self"}
	}
	return nil
}

func (d *Daemon) harnessBusy(name string) bool {
	d.mu.Lock()
	for _, s := range d.sessions {
		if s.info.Harness == name && s.info.Status == "busy" {
			d.mu.Unlock()
			return true
		}
	}
	d.mu.Unlock()
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.harness == name && (w.turnActive || (w.doneStatus == "" && !w.turnHandbackIdle)) {
			return true
		}
	}
	return false
}

func (d *Daemon) refreshUpdateOnly(ctx context.Context) {
	var opts harnesses.Options
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	h := harnesses.UpdateOnly(ctx, "agy", opts)
	if ctx.Err() != nil {
		return
	}
	d.hmu.Lock()
	next := append([]protocol.HarnessInfo{}, d.installed...)
	found := false
	for i := range next {
		if next[i].Name == "agy" {
			found = true
			if h != nil {
				h.Update = next[i].Update
				next[i] = *h
			} else {
				next = append(next[:i], next[i+1:]...)
			}
			break
		}
	}
	if !found && h != nil {
		next = append(next, *h)
	}
	d.installed = next
	d.hgen++
	d.hmu.Unlock()
	d.publishHarnesses(ctx)
}

func (d *Daemon) setHarnessUpdate(ctx context.Context, name string, u *protocol.HarnessUpdate) {
	d.hmu.Lock()
	next := append([]protocol.HarnessInfo{}, d.installed...)
	for i := range next {
		if next[i].Name == name {
			next[i].Update = u
			break
		}
	}
	d.installed = next
	d.hgen++
	d.hmu.Unlock()
	d.Logf("harness update: %s: %s%s", name, u.Result, updateLogDetail(u.Detail))
	d.publishHarnesses(ctx)
}

func updateLogDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return ": " + detail
}

func (d *Daemon) updateHarness(ctx context.Context, h protocol.HarnessInfo) {
	var opts harnesses.Options
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	timeout := updateDelay(d.cfg.HarnessUpdateTimeout, 5*time.Minute)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	out, err := harnesses.RunUpdate(runCtx, h.Path, opts, updateArgs(h.Name)...)
	timedOut := runCtx.Err() == context.DeadlineExceeded
	cancel()
	if ctx.Err() != nil {
		return
	}
	// The regular probe refreshes models, sign-in and version as well as inventory.
	d.refreshHarnessInventory(ctx)
	if h.Name == "agy" {
		d.refreshUpdateOnly(ctx)
	}
	hs, _ := d.inventory()
	after := ""
	for _, current := range hs {
		if current.Name == h.Name {
			after = current.Version
			break
		}
	}
	u := &protocol.HarnessUpdate{CheckedAt: time.Now().UTC(), Before: h.Version, After: after}
	lower := strings.ToLower(out)
	switch {
	case timedOut:
		u.Result = "failed"
		u.Detail = "timed out"
	case h.Name == "opencode" && brewInstall(h.Path) && (strings.Contains(lower, "may be managed by a package manager") || strings.Contains(lower, "installation method not found")):
		u.Result = "update by hand"
		u.Detail = "brew upgrade opencode"
	case err != nil || strings.Contains(lower, "upgrade failed") || strings.Contains(lower, "may be managed by a package manager"):
		u.Result = "failed"
		u.Detail = lastUpdateLine(out)
		if u.Detail == "" && err != nil {
			u.Detail = err.Error()
		}
		if u.Detail == "" {
			u.Detail = "update failed"
		}
	case h.Version == "" || after == "":
		u.Result = "failed"
		u.Detail = "version probe failed"
	case h.Version != after:
		u.Result = "updated"
	default:
		u.Result = "already current"
	}
	d.setHarnessUpdate(ctx, h.Name, u)
}

func brewInstall(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	return strings.Contains(real, "/Cellar/opencode/") || strings.HasPrefix(path, "/opt/homebrew/") || strings.HasPrefix(path, "/usr/local/Homebrew/") || strings.HasPrefix(path, "/home/linuxbrew/")
}

func lastUpdateLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, lines[i]))
		if s != "" {
			if len(s) > 300 {
				s = s[len(s)-300:]
			}
			return s
		}
	}
	return ""
}
