//go:build darwin

package daemon

import (
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// osProcessEnviron reads a process's environment with sysctl
// kern.procargs2.<pid> (pure Go, no cgo, no ps). macOS lets a process read this
// for another process of the same user; for others it fails and nil comes back.
func osProcessEnviron(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil
	}
	return parseProcArgs2(raw)
}

// osProcessArgs reads a process's argument vector from sysctl kern.procargs2.
func osProcessArgs(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil
	}
	return parseProcArgs2Argv(raw)
}

// osProcessList lists the process IDs of this user's processes.
func osProcessList() []int {
	out, err := exec.Command("ps", "-x", "-o", "pid=").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil && pid > 1 {
			pids = append(pids, pid)
		}
	}
	return pids
}
