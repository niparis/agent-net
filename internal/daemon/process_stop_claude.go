package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Claude Code runs its background sessions outside agent-net. Its supervisor
// rewrites a roster file when its live sessions change: each entry
// names the background host (pid) and the session process (replPid). A process
// stop must never end one of them, so processRefusal checks the roster too.
//
// A roster with a supervisor is trusted while that same user process lives.
// When the supervisor cannot be verified, the
// daemon cannot tell which Claude processes are live, so it refuses every
// Claude background process. The daemon_status file is not used: it can be
// hours old.

const (
	// claudeRosterMaxAge is how old the roster may be (in either direction)
	// and still be trusted.
	claudeRosterMaxAge = 60 * time.Second
	// claudeAncestorMax bounds the walk up the parents of a process.
	claudeAncestorMax = 64
)

// claudeRoster is the part of ~/.claude/daemon/roster.json that a process
// stop and the Claude end-of-job check (claudereap.go) read.
type claudeRoster struct {
	SupervisorPID int                     `json:"supervisorPid"`
	UpdatedAt     int64                   `json:"updatedAt"` // Unix milliseconds
	Workers       map[string]claudeWorker `json:"workers"`   // keyed by the short session ID
}

// claudeWorker is one live background session in the roster.
type claudeWorker struct {
	PID           int    `json:"pid"` // the background host
	ProcStart     string `json:"procStart"`
	ReplPID       int    `json:"replPid"` // the session process
	ReplProcStart string `json:"replProcStart"`
	CWD           string `json:"cwd"`
}

// claudeOwner names the session that a roster process belongs to.
type claudeOwner struct {
	short, cwd string
}

// claudeRosterFile is the roster path: $CLAUDE_CONFIG_DIR/daemon/roster.json,
// or ~/.claude/daemon/roster.json when that variable is not set. The same
// path is used on Linux and macOS.
func (d *Daemon) claudeRosterFile() string {
	if d.cfg.claudeRosterPath != "" {
		return d.cfg.claudeRosterPath
	}
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "daemon", "roster.json")
}

// readClaudeRoster verifies the supervisor identity against the write time.
// Legacy rosters without a supervisor retain the bounded freshness check.
func (d *Daemon) readClaudeRoster(now time.Time) *claudeRoster {
	return d.readClaudeRosterWithProcess(now, readProcView)
}

func (d *Daemon) readClaudeRosterWithProcess(now time.Time, read func(int) (procView, error)) *claudeRoster {
	path := d.claudeRosterFile()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r claudeRoster
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil
	}
	if r.SupervisorPID > 1 {
		supervisor, err := read(r.SupervisorPID)
		if err != nil || supervisor.Zombie || supervisor.UID != os.Getuid() || supervisor.EUID != os.Getuid() || supervisor.Started.IsZero() || supervisor.Started.After(time.UnixMilli(r.UpdatedAt)) || r.UpdatedAt > now.UnixMilli() {
			return nil
		}
		return &r
	}
	if age := now.Sub(time.UnixMilli(r.UpdatedAt)); age > claudeRosterMaxAge || age < -claudeRosterMaxAge {
		return nil
	}
	return &r
}

// claudeProcessRefusal says why a process must not be stopped because it is a
// Claude background session, or is inside one, or returns "". A stale or
// missing roster refuses every Claude background process.
func (d *Daemon) claudeProcessRefusal(v procView) string {
	roster := d.readClaudeRoster(time.Now())
	if roster == nil {
		if isClaudeBackgroundCommand(v.Command) {
			return "Claude roster not fresh; cannot tell live sessions"
		}
		return ""
	}
	if roster.SupervisorPID > 1 && roster.SupervisorPID == v.PID {
		return "it is the Claude supervisor of the background sessions"
	}
	owners := map[int]claudeOwner{}
	for short, w := range roster.Workers {
		o := claudeOwner{short: short, cwd: w.CWD}
		for _, pid := range []int{w.PID, w.ReplPID} {
			if pid > 1 {
				owners[pid] = o
			}
		}
	}
	if len(owners) == 0 {
		return ""
	}
	if o, ok := owners[v.PID]; ok {
		return fmt.Sprintf("it is a live Claude background session (short %s, cwd %s)", o.short, o.cwd)
	}
	// A process started by a live session has that session's process in its
	// ancestry. Walk up the parents until one is a roster process or init.
	ppid := v.PPID
	for i := 0; i < claudeAncestorMax && ppid > 1; i++ {
		if o, ok := owners[ppid]; ok {
			return fmt.Sprintf("it runs inside a live Claude background session (short %s, cwd %s)", o.short, o.cwd)
		}
		parent, err := readProcView(ppid)
		if err != nil {
			break
		}
		ppid = parent.PPID
	}
	return ""
}

// isClaudeBackgroundCommand tells if a command line is a Claude Code background
// session or its supervisor: the claude program with a bg- or daemon argument.
func isClaudeBackgroundCommand(command string) bool {
	return strings.Contains(command, "claude bg-") || strings.Contains(command, "claude daemon")
}
