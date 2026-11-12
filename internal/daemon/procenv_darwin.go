//go:build darwin

package daemon

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

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

// lsofTimeout bounds the one lsof call that reads many working directories.
const lsofTimeout = 5 * time.Second

// osProcessCWDs reads the working directory of each process with one lsof call
// (macOS has no /proc, and reading it through libproc needs cgo).
func osProcessCWDs(pids []int) map[int]string {
	if len(pids) == 0 {
		return nil
	}
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	// lsof exits 1 when some pid is gone, and still prints the others.
	out, _ := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(list, ",")).Output()
	return parseLsofCWD(out)
}
