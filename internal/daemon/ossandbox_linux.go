//go:build linux

package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// OSSandboxDefaultOn tells whether Pi and OpenCode workers run in the OS
// sandbox without being asked. On macOS it is off until the profile has been
// run on a Mac (decision 0171, phase 4b): a project opts in with
// workers.os_sandbox in node.json.
const OSSandboxDefaultOn = true

// osSandboxName is the wrapper on this OS, as the permissions line names it.
const osSandboxName = "bubblewrap"

// osSandboxReady checks that bubblewrap is installed and can make a sandbox
// here: a probe runs bwrap itself inside the namespaces a worker gets. It
// fails when user namespaces are turned off (a kernel or AppArmor setting, or
// a container).
func osSandboxReady() error {
	tool, err := exec.LookPath("bwrap")
	if err != nil {
		return fmt.Errorf("bubblewrap (bwrap) is not installed: install it (Debian and Ubuntu: `sudo apt install bubblewrap`, Fedora: `sudo dnf install bubblewrap`)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, "--unshare-all", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", tool, "--version").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("bubblewrap could not make a sandbox (%s): user namespaces may be turned off on this machine (see kernel.unprivileged_userns_clone and kernel.apparmor_restrict_unprivileged_userns)", truncate(msg, 200))
	}
	return nil
}

// osSandboxTool is the wrapper's program: bubblewrap.
func osSandboxTool() (string, error) {
	tool, err := exec.LookPath("bwrap")
	if err != nil {
		return "", fmt.Errorf("bubblewrap is not installed: %w", err)
	}
	return tool, nil
}

// osSandboxCommand is the argument list of the wrapper that runs bin under p.
func osSandboxCommand(p osSandboxPolicy, bin string, args []string) []string {
	return bwrapArgs(p, bin, args)
}

// osSandboxTmpEnv is TMPDIR inside the sandbox. The worker's private tmp folder
// is mounted at /tmp, so the name is the same for every worker.
func osSandboxTmpEnv(string) string { return "/tmp" }
