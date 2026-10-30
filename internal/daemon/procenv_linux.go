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
