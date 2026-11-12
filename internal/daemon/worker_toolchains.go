package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// workerGoBinary finds the go command a worker with this environment would
// run, or returns "" when the machine has none.
func (d *Daemon) workerGoBinary(ctx context.Context, env []string) string {
	opts := harnesses.Options{}
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	if opts.PATH == "" {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "PATH="); ok {
				opts.PATH = v
			}
		}
	}
	bin := harnesses.Executable(ctx, "go", opts)
	if bin == "" {
		bin = standardGo(opts.Home)
	}
	return bin
}

// workerGoModCache is the Go module cache a worker's go command downloads
// into, or "" when the machine has no Go.
func (d *Daemon) workerGoModCache(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env := os.Environ()
	bin := d.workerGoBinary(ctx, env)
	if bin == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, bin, "env", "GOMODCACHE")
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = 250 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if p := strings.TrimSpace(string(out)); filepath.IsAbs(p) {
		return p
	}
	return ""
}

// workerToolchainDirs lists the folders agy's terminal sandbox must expose.
// Keep toolchain probes here so other languages can be added in one place.
func (d *Daemon) workerToolchainDirs(ctx context.Context, dir string, env []string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	bin := d.workerGoBinary(ctx, env)
	if bin == "" || ctx.Err() != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, bin, "env", "GOROOT", "GOMODCACHE")
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = 250 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil
	}
	var dirs []string
	seen := map[string]bool{}
	for _, path := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			continue
		}
		// Reject home and every ancestor, including symlink aliases.
		rel, err := filepath.Rel(real, home)
		if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			continue
		}
		if !seen[real] {
			seen[real] = true
			dirs = append(dirs, path)
		}
	}
	return dirs
}

// Add the actual sandbox grants to both the worker brief and owner notices.
func (d *Daemon) workerToolchainPermissions(w *worker, body string) string {
	d.wmu.Lock()
	level, harness := w.permissions, w.harness
	dirs := append([]string(nil), w.toolchainDirs...)
	d.wmu.Unlock()
	if harness != "agy" || len(dirs) == 0 {
		return body
	}
	old := protocol.WorkerPermissionsText(level, harness)
	added := strings.Replace(old, ")", ", plus toolchains: "+strings.Join(dirs, ", ")+")", 1)
	return strings.Replace(body, old, added, 1)
}

// standardGo finds Go in the folders its installers use when it is not on
// the search path: an owner who installs Go under ~/.local/go adds it to
// PATH only in the project's worker notes.
func standardGo(home string) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	for _, path := range standardGoPaths(home) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return ""
}

// standardGoPaths is a variable so tests can keep the machine's own Go out.
var standardGoPaths = func(home string) []string {
	return []string{filepath.Join(home, ".local", "go", "bin", "go"), filepath.Join(home, "go", "bin", "go"),
		"/usr/local/go/bin/go", "/opt/homebrew/bin/go", "/usr/local/bin/go"}
}
