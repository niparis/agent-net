//go:build linux

package daemon

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// procClockTicks is the unit of the start time in /proc/<pid>/stat (USER_HZ).
// It is 100 on every Linux the program supports.
const procClockTicks = 100

// readProcView reads what process pid is: its owner, parent, command line and
// start time. It returns os.ErrNotExist when there is no such process.
func readProcView(pid int) (procView, error) {
	v := procView{PID: pid}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return v, err
	}
	s := string(raw)
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return v, errors.New("bad /proc stat")
	}
	comm := s[open+1 : closing]
	f := strings.Fields(s[closing+1:]) // state ppid ... starttime is f[19]
	if len(f) < 20 {
		return v, errors.New("bad /proc stat")
	}
	if v.PPID, err = strconv.Atoi(f[1]); err != nil {
		return v, err
	}
	v.Zombie = f[0] == "Z" || f[0] == "X"
	v.Token = f[19]
	ticks, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return v, err
	}
	if boot, ok := procBootTime(); ok {
		v.Started = boot.Add(time.Duration(ticks) * time.Second / procClockTicks).UTC()
	}

	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return v, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			ids := strings.Fields(rest) // real effective saved filesystem
			if len(ids) < 2 {
				return v, errors.New("bad /proc status")
			}
			if v.UID, err = strconv.Atoi(ids[0]); err != nil {
				return v, err
			}
			if v.EUID, err = strconv.Atoi(ids[1]); err != nil {
				return v, err
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil {
					v.RSSKB = kb
				}
			}
		}
	}

	if cmd, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		v.Command = strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))
	}
	if v.Command == "" {
		v.Command = "[" + comm + "]" // a kernel thread or a zombie has no command line
	}
	return v, nil
}

// procBootTime is when the machine booted, from /proc/stat.
func procBootTime() (time.Time, bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(secs, 0), true
		}
	}
	return time.Time{}, false
}

// readProcTable reads every process from /proc. A process that ends while it is
// read is left out.
func readProcTable() (map[int]procView, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	table := make(map[int]procView, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if v, err := readProcView(pid); err == nil {
			table[pid] = v
		}
	}
	return table, nil
}
