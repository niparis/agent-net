//go:build darwin

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// readProcView reads what process pid is: its owner, parent, command line and
// start time, from `ps`. It returns os.ErrNotExist when there is no such
// process.
func readProcView(pid int) (procView, error) {
	v := procView{PID: pid}
	// lstart has spaces, so it is read on its own line.
	out, err := exec.Command("ps", "-ww", "-o", "uid=,ppid=,stat=,lstart=", "-p", strconv.Itoa(pid)).Output()
	line := strings.TrimSpace(string(out))
	if err != nil || line == "" {
		return v, os.ErrNotExist
	}
	f := strings.Fields(line)
	if len(f) < 8 { // uid ppid stat and the five words of lstart
		return v, errors.New("bad ps output")
	}
	if v.UID, err = strconv.Atoi(f[0]); err != nil {
		return v, err
	}
	v.EUID = v.UID // ps prints the effective user
	if v.PPID, err = strconv.Atoi(f[1]); err != nil {
		return v, err
	}
	v.Zombie = strings.HasPrefix(f[2], "Z")
	start := strings.Join(f[3:8], " ")
	v.Token = start
	if t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", start, time.Local); err == nil {
		v.Started = t.UTC()
	}
	cmd, _ := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	v.Command = strings.TrimSpace(string(cmd))
	if v.Command == "" {
		v.Command = "[unknown]"
	}
	return v, nil
}
