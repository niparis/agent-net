package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Leak tracking for the fake harness processes. The fakes are the test binary
// itself, started with setsid by the daemon, so they are not killed when a
// test ends. TestMain gives the run a folder (envFakePids); each long-lived
// fake writes a file named after its pid there. When the tests are done,
// reapFakes kills what is still alive and reports it, so a leak fails the run
// instead of piling up on the machine. A fake also exits by itself when its
// parent is gone, so a killed or timed out test run leaves nothing behind.
const envFakePids = "AGENT_NET_FAKE_PIDS"

// fakeRegister records this fake process and starts the watchdog that makes it
// exit when the process that started it is gone.
func fakeRegister() {
	if dir := os.Getenv(envFakePids); dir != "" {
		os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600)
	}
	ppid := os.Getppid()
	go func() {
		for {
			time.Sleep(time.Second)
			if os.Getppid() != ppid {
				// Kill our process group too: it holds the fake's `sleep` child.
				if pgid, err := syscall.Getpgid(0); err == nil && pgid == os.Getpid() {
					syscall.Kill(-pgid, syscall.SIGKILL)
				}
				os.Exit(1)
			}
		}
	}()
}

// fakeAlive reports whether pid is a live (not zombie) process that belongs to
// the run in dir. The environment check guards against pid reuse.
func fakeAlive(pid int, dir string) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil {
		if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z' {
			return false
		}
	}
	env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	return err == nil && strings.Contains(string(env), envFakePids+"="+dir+"\x00")
}

// reapFakes kills every registered fake that is still alive and returns their
// pids.
func reapFakes(dir string) []int {
	entries, _ := os.ReadDir(dir)
	var alive []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !fakeAlive(pid, dir) {
			continue
		}
		alive = append(alive, pid)
		syscall.Kill(-pid, syscall.SIGKILL) // the fake leads its own group
		syscall.Kill(pid, syscall.SIGKILL)
	}
	return alive
}
