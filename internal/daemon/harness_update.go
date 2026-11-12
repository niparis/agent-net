package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
					if d.updateHarness(ctx, h) {
						// A probe that timed out is retried next round; the
						// harness stays pending so it is not forgotten until
						// the next daily check.
						pending[h.Name] = true
					}
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

func (d *Daemon) updateHarness(ctx context.Context, h protocol.HarnessInfo) (retry bool) {
	var opts harnesses.Options
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	timeout := updateDelay(d.cfg.HarnessUpdateTimeout, 5*time.Minute)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin, args, usedBrew := harnessUpdateCommand(runCtx, h, opts)
	if bin == "" || args == nil {
		if ctx.Err() == nil {
			d.setHarnessUpdate(ctx, h.Name, &protocol.HarnessUpdate{
				CheckedAt: time.Now().UTC(), Before: h.Version, After: h.Version,
				Result: "skipped", Detail: "unknown install method for " + h.Path,
			})
		}
		return false
	}
	out, err := harnesses.RunUpdate(runCtx, bin, opts, args...)
	timedOut := runCtx.Err() == context.DeadlineExceeded
	cancel()
	if ctx.Err() != nil {
		return false
	}
	// The regular probe refreshes models, sign-in and version as well as inventory.
	d.refreshHarnessInventory(ctx)
	if ctx.Err() != nil {
		return false
	}
	hs, _ := d.inventory()
	after := ""
	for _, current := range hs {
		if current.Name == h.Name {
			after = current.Version
			break
		}
	}
	// The inventory probe can come back empty under load. Probe once more so
	// the report can tell a timeout from a probe that ran and failed.
	var probeErr error
	if after == "" && h.Path != "" {
		after, probeErr = harnesses.VersionProbe(ctx, h.Path, opts)
		if ctx.Err() != nil {
			return false
		}
	}
	u := &protocol.HarnessUpdate{CheckedAt: time.Now().UTC(), Before: h.Version, After: after}
	lower := strings.ToLower(out)
	switch {
	case timedOut:
		u.Result = "failed"
		u.Detail = "timed out"
	case err != nil || strings.Contains(lower, "upgrade failed") || strings.Contains(lower, "update failed"):
		detail := lastUpdateLine(out)
		if detail == "" && err != nil {
			detail = err.Error()
		}
		if detail == "" {
			detail = "update failed"
		}
		u.Result = "failed"
		u.Detail = detail
	case !usedBrew && packageManagerOutput(lower):
		// The self-updater did nothing because a package manager owns the
		// install. Report what happened instead of "already current".
		u.Result = "failed"
		u.Detail = firstNonEmpty(lastUpdateLine(out), "installed by a package manager")
	case after == "":
		u.Result, u.Detail = probeFailureResult(probeErr)
		retry = u.Result == "timed out, retrying"
	case h.Version != after:
		u.Result = "updated"
	default:
		u.Result = "already current"
	}
	d.setHarnessUpdate(ctx, h.Name, u)
	return retry
}

// probeFailureResult says what an empty version probe means. A probe stopped by
// its deadline is retried on the next round, because load is not a broken
// harness. A probe that ran and failed, or printed nothing, is a failure.
func probeFailureResult(err error) (result, detail string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out, retrying", "version probe timed out"
	}
	return "failed", "version probe failed"
}

// packageManagerOutput says whether an updater deferred to a package manager,
// from its own words. agent-net upgrades those copies with brew instead.
func packageManagerOutput(lower string) bool {
	for _, marker := range []string{
		"package manager",
		"brew upgrade",
		"installed by homebrew",
		"installed via homebrew",
		"managed by homebrew",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// Installation ownership comes from the resolved binary, not its command name:
// a Homebrew bin directory can also contain npm links and native installers.
func harnessUpdateCommand(ctx context.Context, h protocol.HarnessInfo, opts harnesses.Options) (string, []string, bool) {
	real := realPath(h.Path)
	brew := harnesses.Executable(ctx, "brew", opts)
	candidate := brewInstall(h.Path) || (brew != "" && filepath.Dir(h.Path) == filepath.Dir(brew))
	if candidate && brew != "" {
		if args := installedBrewArgs(ctx, brew, h.Path, real, opts); args != nil {
			return brew, args, true
		}
	}
	if strings.Contains(real, "/node_modules/") {
		if npm := harnesses.Executable(ctx, "npm", opts); npm != "" {
			if pkg := npmGlobalPackage(ctx, npm, real, h.Name, opts); pkg != "" {
				return npm, []string{"install", "--global", pkg + "@latest"}, false
			}
		}
		return "", nil, false
	}
	if candidate {
		return "", nil, false
	}
	return h.Path, updateArgs(h.Name), false
}

// Brew metadata can exceed RunUpdate's log tail. Discovery needs the full JSON
// and shares the update deadline and process-group cancellation.
func installProbe(ctx context.Context, bin string, opts harnesses.Options, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = os.TempDir()
	path := opts.PATH
	if path == "" {
		path = os.Getenv("PATH")
	}
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	return string(out), err
}

func brewPathPackage(real, room string) string {
	_, tail, ok := strings.Cut(real, "/"+room+"/")
	if !ok {
		return ""
	}
	pkg, _, ok := strings.Cut(tail, "/")
	if !ok {
		return ""
	}
	return pkg
}

func installedBrewArgs(ctx context.Context, brew, path, real string, opts harnesses.Options) []string {
	formula, cask := brewPathPackage(real, "Cellar"), brewPathPackage(real, "Caskroom")
	var info struct {
		Formulae []struct {
			Name      string            `json:"name"`
			FullName  string            `json:"full_name"`
			Installed []json.RawMessage `json:"installed"`
		} `json:"formulae"`
		Casks []struct {
			Token     string          `json:"token"`
			FullToken string          `json:"full_token"`
			Installed json.RawMessage `json:"installed"`
			Artifacts []struct {
				Binary []json.RawMessage `json:"binary"`
			} `json:"artifacts"`
		} `json:"casks"`
	}
	out, err := installProbe(ctx, brew, opts, "info", "--json=v2", "--installed")
	if err == nil && json.Unmarshal([]byte(out), &info) == nil {
		for _, f := range info.Formulae {
			if formula != "" && f.Name == formula && len(f.Installed) > 0 {
				return []string{"upgrade", firstNonEmpty(f.FullName, f.Name)}
			}
		}
		for _, c := range info.Casks {
			if len(c.Installed) == 0 || string(c.Installed) == "null" || string(c.Installed) == "[]" {
				continue
			}
			owned := cask != "" && c.Token == cask
			for _, artifact := range c.Artifacts {
				if len(artifact.Binary) == 0 {
					continue
				}
				var source string
				if json.Unmarshal(artifact.Binary[0], &source) == nil && filepath.IsAbs(source) && realPath(source) == real {
					owned = true
				}
				// Casks can link a native install outside Caskroom. An absolute
				// artifact target also proves ownership when the source is relative.
				if len(artifact.Binary) > 1 {
					var target struct {
						Target string `json:"target"`
					}
					if json.Unmarshal(artifact.Binary[1], &target) == nil && filepath.IsAbs(target.Target) && filepath.Clean(target.Target) == filepath.Clean(path) {
						owned = true
					}
				}
			}
			if owned {
				return []string{"upgrade", "--cask", firstNonEmpty(c.FullToken, c.Token)}
			}
		}
	}
	// Cellar and Caskroom encode the actual installed package, even if brew
	// cannot return metadata. A bin prefix alone never identifies a package.
	if formula != "" {
		return []string{"upgrade", formula}
	}
	if cask != "" {
		return []string{"upgrade", "--cask", cask}
	}
	return nil
}

func npmGlobalPackage(ctx context.Context, npm, real, name string, opts harnesses.Options) string {
	prefix, tail, ok := strings.Cut(real, "/node_modules/")
	if !ok {
		return ""
	}
	parts := strings.Split(tail, "/")
	count := 1
	if strings.HasPrefix(parts[0], "@") {
		count = 2
	}
	if len(parts) <= count {
		return ""
	}
	root := prefix + "/node_modules"
	out, err := installProbe(ctx, npm, opts, "root", "-g")
	if err != nil || realPath(strings.TrimSpace(out)) != realPath(root) {
		return ""
	}
	dir := filepath.Join(root, filepath.Join(parts[:count]...))
	var pkg struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Name != strings.Join(parts[:count], "/") {
		return ""
	}
	var bin string
	if json.Unmarshal(pkg.Bin, &bin) != nil {
		var bins map[string]string
		if json.Unmarshal(pkg.Bin, &bins) != nil {
			return ""
		}
		bin = bins[name]
	}
	if bin == "" || realPath(filepath.Join(dir, bin)) != real {
		return ""
	}
	return pkg.Name
}

func realPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func brewInstall(path string) bool {
	real := realPath(path)
	if strings.Contains(real, "/Cellar/") || strings.Contains(real, "/Caskroom/") {
		return true
	}
	return strings.HasPrefix(path, "/opt/homebrew/") || strings.HasPrefix(path, "/usr/local/Homebrew/") || strings.HasPrefix(path, "/home/linuxbrew/")
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
