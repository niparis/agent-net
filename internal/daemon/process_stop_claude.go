package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code runs its background sessions outside agent-net. Its supervisor
// rewrites a roster file when its live sessions change: each entry
// names the background host (pid) and the session process (replPid). A process
// stop must never end one of them, so processRefusal checks the roster too.
//
// A roster with a supervisor is trusted while that same user process lives.
// When the supervisor cannot be verified, the daemon cannot tell which Claude processes are live, so it refuses every
// Claude background process. The daemon_status file is not used: it can be
// hours old.

const (
	// claudeRosterMaxAge applies only to legacy rosters without a supervisor.
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
	SessionID     string `json:"sessionId"`
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

// readClaudeRosterWithProcess reads the roster. The supervisor is looked up
// with read: the live reader for a process stop, a snapshot for the list.
func (d *Daemon) readClaudeRosterWithProcess(now time.Time, read procLookup) *claudeRoster {
	r, _ := d.claudeRosterStatus(now, read)
	return r
}

// claudeRosterStatus reads the roster once and says if it can be trusted. The
// roster is nil unless the status is fresh. The status carries the age of the
// file and the reason, so a person can see why every Claude process is refused.
func (d *Daemon) claudeRosterStatus(now time.Time, read procLookup) (*claudeRoster, protocol.ClaudeRosterStatus) {
	st := protocol.ClaudeRosterStatus{State: protocol.ClaudeRosterMissing}
	path := d.claudeRosterFile()
	if path == "" {
		st.Note = "there is no home folder, so there is no roster path"
		return nil, st
	}
	readFile := d.cfg.readClaudeRosterFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	raw, err := readFile(path)
	if err != nil {
		st.Note = "the roster file cannot be read"
		if os.IsNotExist(err) {
			st.Note = "the roster file does not exist"
		}
		return nil, st
	}
	var r claudeRoster
	if err := json.Unmarshal(raw, &r); err != nil {
		st.Note = "the roster file is not valid JSON"
		return nil, st
	}
	st.Sessions = len(r.Workers)
	if r.UpdatedAt > 0 {
		st.UpdatedAt = time.UnixMilli(r.UpdatedAt).UTC().Format(time.RFC3339)
		st.AgeSeconds = max(0, int64(now.Sub(time.UnixMilli(r.UpdatedAt))/time.Second))
	}
	if r.SupervisorPID > 1 {
		st.SupervisorPID = r.SupervisorPID
		st.State = protocol.ClaudeRosterUnverified
		supervisor, err := read(r.SupervisorPID)
		switch {
		case err != nil:
			st.Note = fmt.Sprintf("supervisor pid %d is not running", r.SupervisorPID)
		case supervisor.Zombie:
			st.Note = fmt.Sprintf("supervisor pid %d has ended", r.SupervisorPID)
		case supervisor.UID != os.Getuid() || supervisor.EUID != os.Getuid():
			st.Note = fmt.Sprintf("supervisor pid %d is not a process of this user", r.SupervisorPID)
		case supervisor.Started.IsZero():
			st.Note = fmt.Sprintf("the start time of supervisor pid %d is unknown", r.SupervisorPID)
		case supervisor.Started.After(time.UnixMilli(r.UpdatedAt)):
			st.Note = fmt.Sprintf("supervisor pid %d started after the roster was written, so the file is from an older supervisor", r.SupervisorPID)
		case r.UpdatedAt > now.UnixMilli():
			st.Note = "the roster file is dated in the future"
		default:
			st.State = protocol.ClaudeRosterFresh
			st.Note = fmt.Sprintf("supervisor pid %d is alive and older than the file. The file changes only when sessions change, so its age does not matter", r.SupervisorPID)
			return &r, st
		}
		return nil, st
	}
	if age := now.Sub(time.UnixMilli(r.UpdatedAt)); age > claudeRosterMaxAge || age < -claudeRosterMaxAge {
		st.State = protocol.ClaudeRosterStale
		st.Note = "the roster names no supervisor and was written more than a minute ago"
		return nil, st
	}
	st.State = protocol.ClaudeRosterFresh
	st.Note = "the roster names no supervisor and was written within the last minute"
	return &r, st
}

// claudeRosterProcessRefusal says why a process must not be stopped because it
// is a Claude background session, or is inside one, or returns "". A nil roster
// refuses every Claude background process. The ancestors of v are looked up
// with lookup.
func claudeRosterProcessRefusal(v procView, roster *claudeRoster, lookup procLookup) string {
	if roster == nil {
		if isClaudeBackgroundCommand(v.Command) {
			return "Claude roster not fresh; cannot tell live sessions (missing, invalid, or supervisor not verified alive)"
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
		parent, err := lookup(ppid)
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
