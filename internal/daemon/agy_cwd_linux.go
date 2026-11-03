//go:build linux

package daemon

import (
	"os"
	"strconv"
	"strings"
)

func agyProcessCwd(pid int) string {
	p, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	return p
}

// start time distinguishes a reused PID from the process that ran the hook.
func agyProcessStart(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) <= 19 {
		return ""
	}
	return fields[19]
}
