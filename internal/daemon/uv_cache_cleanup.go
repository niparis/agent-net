package daemon

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

const (
	// uvLockTimeout is the wait uv gets for another uv process, in seconds.
	uvLockTimeout = "10"
	// uvPruneTimeout bounds one uv cache prune; uv is this daemon's child.
	uvPruneTimeout = 30 * time.Second
)

// pruneUVCache runs `uv cache prune` when uv is on PATH and the setting is on.
// uv ends its own lock wait through UV_LOCK_TIMEOUT; the daemon never uses
// --force or `uv cache clean`.
func (d *Daemon) pruneUVCache(ctx context.Context, now time.Time) {
	if !d.SettingsSnapshot().Bool("cleanup.uv_prune", settings.Where{}) {
		return
	}
	bin, err := exec.LookPath("uv")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, uvPruneTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "cache", "prune")
	cmd.Env = workerEnv(os.Environ(), map[string]string{"UV_LOCK_TIMEOUT": uvLockTimeout, "LC_ALL": "C"})
	out, err := cmd.CombinedOutput()
	if err != nil {
		if uvLockTimedOut(string(out)) && d.log != nil && d.shouldLogUVTimeout(now) {
			d.Logf("uv cache prune: lock timeout; skipped this run")
		}
		return
	}
	if d.log == nil {
		return
	}
	if line := uvSummaryLine(string(out)); line != "" {
		d.Logf("uv cache prune: %s", line)
		return
	}
	d.Logf("uv cache prune: done")
}

// shouldLogUVTimeout keeps a lock timeout to one log line a day.
func (d *Daemon) shouldLogUVTimeout(now time.Time) bool {
	d.uvTimeoutMu.Lock()
	defer d.uvTimeoutMu.Unlock()
	if !d.uvTimeoutLogged.IsZero() && now.Sub(d.uvTimeoutLogged) < 24*time.Hour {
		return false
	}
	d.uvTimeoutLogged = now
	return true
}

func uvLockTimedOut(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "timeout") && strings.Contains(lower, "lock")
}

// uvSummaryLine returns the removal summary uv prints on success, as given.
func uvSummaryLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Removed ") || line == "No unused entries found" {
			return line
		}
	}
	return ""
}
