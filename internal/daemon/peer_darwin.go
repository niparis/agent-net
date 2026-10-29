//go:build darwin

package daemon

import (
	"errors"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	solLocal     = 0     // SOL_LOCAL
	localPeerPID = 0x002 // LOCAL_PEERPID
)

// peerPID returns the PID of the process on the other end (LOCAL_PEERPID).
func peerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix connection")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var serr error
	if err := rc.Control(func(fd uintptr) {
		pid, serr = syscall.GetsockoptInt(int(fd), solLocal, localPeerPID)
	}); err != nil {
		return 0, err
	}
	return pid, serr
}

func ps(field string, pid int) (string, error) {
	out, err := exec.Command("ps", "-o", field+"=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(out)), err
}

func parentPID(pid int) (int, error) {
	s, err := ps("ppid", pid)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

func procCmdline(pid int) string {
	s, _ := ps("command", pid)
	return s
}

// procExe returns the path of the process executable, or "".
func procExe(pid int) string {
	s, _ := ps("comm", pid)
	return s
}
