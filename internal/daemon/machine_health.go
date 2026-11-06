package daemon

import (
	"os"
	"os/exec"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// machineHealth describes the daemon's own environment, not the login shell.
func (d *Daemon) machineHealth() *protocol.MachineHealth {
	h := &protocol.MachineHealth{Status: "reported", RestartTime: d.startedAt.UTC().Format(time.RFC3339), Path: os.Getenv("PATH"), Helpers: make(map[string]string)}
	for _, name := range []string{"herdr", "claude", "codex", "opencode", "pi", "git", "gh", "ssh"} {
		path, err := exec.LookPath(name)
		if err != nil {
			path = "not found"
		}
		h.Helpers[name] = path
	}
	if free, ok := diskFree(d.cfg.StateDir); ok {
		h.StateFreeBytes = &free
	}
	return h
}
