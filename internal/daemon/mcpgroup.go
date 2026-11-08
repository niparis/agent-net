package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A hosted stdio server runs in its own process group, so the host can end the
// server and everything it started (uv starts python, npx starts node). The
// group's leader pid is written to a record file in the state folder as soon as
// the server starts. If the daemon dies before it can end the group, the next
// daemon reads the records and ends those groups. A record holds the pid and
// the process start time, so a pid that was reused by another program is never
// touched.

const (
	mcpPidDirName = "mcp-pids"
	// mcpGroupGrace is how long a group gets after SIGTERM before SIGKILL.
	mcpGroupGrace = 2 * time.Second
)

// procStartID names one run of a process: its start time, as the system gives
// it. It is empty when the process does not exist.
func procStartID(pid int) string {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			return ""
		}
		f := strings.Fields(s[i+1:])
		// After the command name the fields start at "state" (field 3); the
		// start time is field 22, so index 19 here.
		if len(f) > 19 {
			return f[19]
		}
		return ""
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(out)), "_")
}

func groupExists(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// endGroup ends a process group: SIGTERM, then SIGKILL for what is left after
// the grace time. It returns when the group is gone.
func endGroup(pgid int, grace time.Duration) {
	if pgid <= 1 {
		return
	}
	syscall.Kill(-pgid, syscall.SIGTERM)
	waitGroupGone(pgid, grace)
	if groupExists(pgid) {
		syscall.Kill(-pgid, syscall.SIGKILL)
		waitGroupGone(pgid, grace)
	}
}

func waitGroupGone(pgid int, max time.Duration) {
	deadline := time.Now().Add(max)
	for groupExists(pgid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
}

func (h *mcpHost) pidDir() string {
	if h.d == nil || h.d.cfg.StateDir == "" {
		return ""
	}
	return filepath.Join(h.d.cfg.StateDir, mcpPidDirName)
}

func writeMCPPidRecord(dir string, pid int) {
	if dir == "" {
		return
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	name := filepath.Join(dir, strconv.Itoa(pid))
	tmp := name + ".tmp"
	if os.WriteFile(tmp, []byte(fmt.Sprintf("%d %s\n", pid, procStartID(pid))), 0o600) == nil {
		os.Rename(tmp, name)
	}
}

// reapLeftovers ends server groups that an earlier daemon started and did not
// end. It touches only groups named in a record this daemon family wrote.
func (h *mcpHost) reapLeftovers() {
	dir := h.pidDir()
	if dir == "" {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), ".tmp") {
			os.Remove(path)
			continue
		}
		b, err := os.ReadFile(path)
		f := strings.Fields(string(b))
		pid := 0
		if err == nil && len(f) >= 1 {
			pid, _ = strconv.Atoi(f[0])
		}
		if pid <= 1 {
			os.Remove(path)
			continue
		}
		recorded := ""
		if len(f) >= 2 {
			recorded = f[1]
		}
		now := procStartID(pid)
		switch {
		case now != "" && (recorded == "" || now != recorded):
			// The pid belongs to another program now.
		case groupExists(pid):
			// Either the recorded leader is alive, or it is gone and members
			// keep the group (the kernel does not reuse the number meanwhile).
			h.d.Logf("mcp host: ending a server group left by an earlier daemon (pgid %d)", pid)
			endGroup(pid, mcpGroupGrace)
		}
		os.Remove(path)
	}
}

// recordingTransport starts the server through inner and reports its pid at
// once, before the MCP handshake.
type recordingTransport struct {
	inner   *mcp.CommandTransport
	started func(pid int)
}

func (t *recordingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.inner.Connect(ctx)
	if p := t.inner.Command.Process; p != nil {
		t.started(p.Pid)
	}
	return c, err
}
