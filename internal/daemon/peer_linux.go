//go:build linux

package daemon

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// peerPID returns the PID of the process on the other end (SO_PEERCRED).
func peerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix connection")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := rc.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if serr != nil {
		return 0, serr
	}
	return int(cred.Pid), nil
}

func parentPID(pid int) (int, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, errors.New("bad /proc stat")
	}
	f := strings.Fields(s[i+1:]) // state ppid ...
	if len(f) < 2 {
		return 0, errors.New("bad /proc stat")
	}
	return strconv.Atoi(f[1])
}

func procCmdline(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(raw), "\x00", " ")
}

// procExe returns the path of the process executable, or "".
func procExe(pid int) string {
	p, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(p, " (deleted)")
}
