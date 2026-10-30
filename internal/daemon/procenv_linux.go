//go:build linux

package daemon

import (
	"bytes"
	"os"
	"strconv"
)

// processEnvVar reads one variable of a process's environment from /proc. It
// returns "" when the process is not ours or the variable is not set.
func processEnvVar(pid int, key string) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	prefix := []byte(key + "=")
	for _, kv := range bytes.Split(raw, []byte{0}) {
		if bytes.HasPrefix(kv, prefix) {
			return string(kv[len(prefix):])
		}
	}
	return ""
}
