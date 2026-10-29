//go:build darwin

package daemon

import "strings"

// procToken identifies one run of a process: its start time as `ps` prints
// it. A reused process ID has a different token. alive is false when the
// process is gone or is a zombie.
func procToken(pid int) (token string, alive bool) {
	start, err := ps("lstart", pid)
	if err != nil || start == "" {
		return "", false
	}
	state, _ := ps("stat", pid)
	return start, !strings.HasPrefix(state, "Z")
}
