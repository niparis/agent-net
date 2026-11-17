//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// OSSandboxDefaultOn tells whether Pi and OpenCode workers run in the OS
// sandbox without being asked. Linux and macOS both do since 2026-11-17: the
// profile passed its real check on a Mac on that day (decision 0171, phase 4b).
// A project turns it off with workers.os_sandbox_off in node.json.
const OSSandboxDefaultOn = true

// osSandboxName is the wrapper on this OS, as the permissions line names it.
const osSandboxName = "sandbox-exec"

const sandboxExecPath = "/usr/bin/sandbox-exec"

// osSandboxReady checks that sandbox-exec exists and that a probe profile runs
// here. It fails when the tool was removed, or when the daemon itself already
// runs in a sandbox (Seatbelt profiles do not nest).
func osSandboxReady() error {
	tool, err := osSandboxTool()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("sandbox-exec could not run a probe profile (%s): the daemon may already run inside a sandbox", truncate(msg, 200))
	}
	return nil
}

// osSandboxTool is the wrapper's program: /usr/bin/sandbox-exec.
func osSandboxTool() (string, error) {
	if _, err := os.Stat(sandboxExecPath); err != nil {
		return "", fmt.Errorf("%s is missing: it ships with macOS, so this macOS has removed it and the OS sandbox cannot run (turn it off for the project with workers.os_sandbox_off in node.json)", sandboxExecPath)
	}
	return sandboxExecPath, nil
}

// osSandboxCommand is the argument list of the wrapper that runs bin under p.
func osSandboxCommand(p osSandboxPolicy, bin string, args []string) []string {
	return seatbeltArgs(p, bin, args)
}

// osSandboxTmpEnv is TMPDIR inside the sandbox. macOS has no mount namespace,
// so the private tmp folder is named by its real path.
func osSandboxTmpEnv(tmp string) string { return tmp }
