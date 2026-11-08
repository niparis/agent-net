package daemon

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

// uvLockTimeout is the wait uv gets for another uv process, in seconds.
const uvLockTimeout = "10"

// uvBytesRE reads the removal summary of uv cache prune, such as
// "Removed 2 files (1.5MiB)" or "(at least 1.0GiB)".
var uvBytesRE = regexp.MustCompile(`\((?:(?:at least )?)([0-9]+(?:\.[0-9]+)?)(B|KiB|MiB|GiB|TiB|PiB|EiB)\)`)

// pruneUVCache runs `uv cache prune` when uv is on PATH and the setting is on.
// uv ends its own lock wait through UV_LOCK_TIMEOUT; the daemon never kills a
// process and never uses --force or `uv cache clean`.
func (d *Daemon) pruneUVCache(now time.Time) {
	if !d.SettingsSnapshot().Bool("cleanup.uv_prune", settings.Where{}) {
		return
	}
	bin, err := exec.LookPath("uv")
	if err != nil {
		return
	}
	cmd := exec.Command(bin, "cache", "prune")
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
	if freed, ok := parseUVFreedBytes(string(out)); ok {
		d.Logf("uv cache prune: %d bytes freed", freed)
		return
	}
	d.Logf("uv cache prune: done (uv reported no byte count)")
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
	return strings.Contains(strings.ToLower(out), "when waiting for lock")
}

func parseUVFreedBytes(out string) (uint64, bool) {
	m := uvBytesRE.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	mult := uint64(1)
	switch m[2] {
	case "KiB":
		mult = 1 << 10
	case "MiB":
		mult = 1 << 20
	case "GiB":
		mult = 1 << 30
	case "TiB":
		mult = 1 << 40
	case "PiB":
		mult = 1 << 50
	case "EiB":
		mult = 1 << 60
	}
	return uint64(value * float64(mult)), true
}
