package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude worker's shell commands run in Claude Code's own Bash sandbox
// (claudeWorkerSandbox). On Linux that sandbox is bubblewrap plus socat. When
// one is missing, Claude Code does not start it: it warns that its dependencies
// are missing and runs the commands with no OS sandbox. The worker's notice
// must not say "sandboxed" then (research machine, 2026-11-21).

// claudeSandboxTools are the programs Claude Code's Linux sandbox needs.
var claudeSandboxTools = []string{"bwrap", "socat"}

// claudeSandboxMissing lists the tools of claudeSandboxTools that are not
// executable in the PATH list. Only Linux needs them: the sandbox of macOS
// ships with the OS.
func claudeSandboxMissing(goos, path string) []string {
	if goos != "linux" {
		return nil
	}
	var missing []string
	for _, name := range claudeSandboxTools {
		if findExecutable(name, path) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

// findExecutable is the path of the first executable regular file called name
// in the PATH list, or "" when there is none.
func findExecutable(name, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// claudeSandboxState is the sandbox mark of a Claude worker that starts now:
// "" when Claude Code's sandbox has its tools, else the mark that makes the
// worker's notice say it runs with no OS sandbox. The start is not refused:
// the owner has not asked for a gate. The gap is logged once per daemon start.
func (d *Daemon) claudeSandboxState() string {
	missing := claudeSandboxMissing(runtime.GOOS, d.workerSearchPATH())
	if len(missing) == 0 {
		return ""
	}
	d.claudeSandboxLog.Do(func() {
		d.Logf("claude workers: Claude Code's sandbox needs %s on the worker PATH, and it is missing here: Claude workers run their shell commands with no OS sandbox, and their notice says so", strings.Join(missing, " and "))
	})
	return protocol.OSSandboxNoneClaude(missing)
}
