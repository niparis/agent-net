//go:build linux

package daemon

import (
	"bytes"
	"os"
	"strconv"
)

// osProcessEnviron reads a process's environment from /proc. It returns nil when
// the process is not ours or is gone.
func osProcessEnviron(pid int) []string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil
	}
	var out []string
	for _, kv := range bytes.Split(raw, []byte{0}) {
		if len(kv) > 0 {
			out = append(out, string(kv))
		}
	}
	return out
}

// osProcessArgs reads a process's argument vector from /proc. It returns nil
// when the process is not ours or is gone.
func osProcessArgs(pid int) []string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil
	}
	var out []string
	for _, arg := range bytes.Split(raw, []byte{0}) {
		if len(arg) > 0 {
			out = append(out, string(arg))
		}
	}
	return out
}

// osProcessList lists the process IDs in /proc.
func osProcessList() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil && pid > 1 {
			out = append(out, pid)
		}
	}
	return out
}

// osProcessCWDs reads the working directory of each process from /proc.
func osProcessCWDs(pids []int) map[int]string {
	res := make(map[int]string, len(pids))
	for _, pid := range pids {
		if cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd"); err == nil {
			res[pid] = cwd
		}
	}
	return res
}
