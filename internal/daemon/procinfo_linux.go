//go:build linux

package daemon

import (
	"os"
	"strconv"
	"strings"
)

// procToken identifies one run of a process: its start time in clock ticks
// since boot. A reused process ID has a different token. alive is false when
// the process is gone or is a zombie.
func procToken(pid int) (token string, alive bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	f := strings.Fields(s[i+1:]) // state ppid pgrp session ... starttime is field 22 of the whole line
	if len(f) < 20 {
		return "", false
	}
	return f[19], f[0] != "Z" && f[0] != "X"
}
