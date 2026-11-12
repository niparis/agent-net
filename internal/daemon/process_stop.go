package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO stops processes on a machine on the owner's instruction (decision
// 0102). It is for orphans: processes that nobody owns any more. No undo is
// possible, so the result and the Inbox notice say exactly what each process
// was before it was signalled.

const (
	// processStopGrace is how long a process has to end after SIGTERM before it
	// gets SIGKILL.
	processStopGrace = 5 * time.Second
	// processKillWait is how long to wait for a process to end after SIGKILL.
	processKillWait = 2 * time.Second
	processPoll     = 25 * time.Millisecond
	// processCommandMax bounds the command line kept in the result, the log and
	// the Inbox notice.
	processCommandMax = 400
)

// procView is what the machine says a process is, read before any signal. It
// is filled by readProcView (procview_linux.go, procview_darwin.go).
type procView struct {
	PID, PPID, UID, EUID int
	// Command is the whole command line, or [name] for a process without one.
	Command string
	Started time.Time
	// Token identifies one run of the process: a reused process ID has another.
	Token  string
	Zombie bool
	// RSSKB is the resident set size in kilobytes, or 0 when it cannot be read.
	RSSKB int64
}

// processStop stops processes on the machine q.NodeID. The machine is always
// named: it never means "this machine" when empty, because a stop on the wrong
// machine is the mistake this tool exists to avoid. A process on another
// machine travels as an owner frame, like a folder trust, and that machine
// checks the owner flag again.
func (d *Daemon) processStop(ctx context.Context, q protocol.ProcessStopRequest) (protocol.ProcessStopResult, error) {
	q.NodeID = strings.TrimSpace(q.NodeID)
	if q.NodeID == "" {
		return protocol.ProcessStopResult{}, errors.New("process_stop needs node_id: name the machine that runs the processes")
	}
	q.PIDs = distinctPIDs(q.PIDs)
	if len(q.PIDs) == 0 {
		return protocol.ProcessStopResult{}, errors.New("process_stop needs at least one pid")
	}
	if q.NodeID == d.cfg.NodeID || strings.EqualFold(q.NodeID, d.cfg.Hostname) || strings.EqualFold(q.NodeID, d.hostName()) {
		return d.processStopLocal(ctx, q.PIDs)
	}
	owner := d.ownerSessionID()
	if owner == "" {
		return protocol.ProcessStopResult{}, errors.New("the owner session is not registered yet (is the gateway reachable?)")
	}
	data, err := d.call(ctx, protocol.FrameSessionStopProcess, protocol.SessionProcessStopRelay{Owner: owner, ProcessStopRequest: q})
	if err != nil {
		return protocol.ProcessStopResult{}, tooOldGateway(err)
	}
	var res protocol.ProcessStopResult
	if err := json.Unmarshal(data, &res); err != nil {
		return protocol.ProcessStopResult{}, err
	}
	return res, nil
}

// distinctPIDs keeps the first of each process ID, in order.
func distinctPIDs(in []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, pid := range in {
		if !seen[pid] {
			seen[pid] = true
			out = append(out, pid)
		}
	}
	return out
}

// onProcessStopFrame serves the owner's process stop from another machine.
func (d *Daemon) onProcessStopFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p protocol.SessionOwnerProcessStop
	var res protocol.ProcessStopResult
	err := json.Unmarshal(raw, &p)
	switch {
	case err != nil:
		err = errors.New("bad process stop relay")
	default:
		if err = d.checkOwnerRelay(ctx, p.Owner, p.FromNode); err == nil {
			if p.NodeID != d.cfg.NodeID {
				err = errors.New("this process stop is for another machine")
			} else if pids := distinctPIDs(p.PIDs); len(pids) == 0 {
				err = errors.New("process_stop needs at least one pid")
			} else {
				res, err = d.processStopLocal(ctx, pids)
			}
		}
	}
	if out, eerr := protocol.EncodeResult(h.ID, res, err); eerr == nil {
		l.write(ctx, out)
	}
}

// stopTarget is one process ID on its way through a stop.
type stopTarget struct {
	out     protocol.ProcessOutcome
	token   string
	allowed bool
	termed  bool
}

// processStopLocal stops processes on this machine: it reads and judges each
// one, sends SIGTERM to the allowed ones, waits, then sends SIGKILL to any that
// are still alive.
func (d *Daemon) processStopLocal(ctx context.Context, pids []int) (protocol.ProcessStopResult, error) {
	res := protocol.ProcessStopResult{NodeID: d.cfg.NodeID, Hostname: d.hostName()}
	targets := make([]*stopTarget, len(pids))
	for i, pid := range pids {
		targets[i] = d.inspectForStop(pid)
	}

	// SIGTERM to every allowed process, then one shared wait.
	var live []*stopTarget
	for _, t := range targets {
		if !t.allowed {
			continue
		}
		if !t.sameRun() {
			t.out.Outcome, t.out.Reason = protocol.ProcessNotFound, "it ended before it could be signalled"
			continue
		}
		switch err := d.sendProcessSignal(t.out.PID, syscall.SIGTERM); {
		case err == nil:
			t.termed = true
			live = append(live, t)
		case errors.Is(err, syscall.ESRCH):
			t.out.Outcome, t.out.Reason = protocol.ProcessNotFound, "it ended before it could be signalled"
		default:
			t.out.Outcome, t.out.Reason = protocol.ProcessFailed, "SIGTERM failed: "+err.Error()
		}
	}
	waitProcessesGone(ctx, live, d.processGrace())

	// SIGKILL to what is still the same run and still alive.
	var killed []*stopTarget
	for _, t := range live {
		if !t.sameRun() {
			t.out.Outcome = protocol.ProcessStoppedTerm
			continue
		}
		if ctx.Err() != nil {
			t.out.Outcome, t.out.Reason = protocol.ProcessFailed, "the request ended before the wait was over; SIGTERM was sent"
			continue
		}
		switch err := d.sendProcessSignal(t.out.PID, syscall.SIGKILL); {
		case err == nil:
			killed = append(killed, t)
		case errors.Is(err, syscall.ESRCH):
			t.out.Outcome = protocol.ProcessStoppedTerm
		default:
			t.out.Outcome, t.out.Reason = protocol.ProcessFailed, "SIGKILL failed: "+err.Error()
		}
	}
	waitProcessesGone(ctx, killed, processKillWait)
	for _, t := range killed {
		if t.sameRun() {
			t.out.Outcome, t.out.Reason = protocol.ProcessFailed, "it is still there after SIGKILL (it may be stuck in the kernel)"
		} else {
			t.out.Outcome = protocol.ProcessStoppedKill
		}
	}

	for _, t := range targets {
		res.Processes = append(res.Processes, t.out)
		o := t.out
		d.Logf("process.stop pid=%d outcome=%s ppid=%d started=%s command=%q reason=%q", o.PID, o.Outcome, o.ParentPID, o.Started, o.Command, o.Reason)
	}
	return res, nil
}

func (d *Daemon) processGrace() time.Duration {
	if d.cfg.testProcessStopGrace > 0 {
		return d.cfg.testProcessStopGrace
	}
	return processStopGrace
}

// sendProcessSignal is the only place that signals. It never signals a process
// group (a pid below 2), whatever the caller passes.
func (d *Daemon) sendProcessSignal(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	if d.cfg.testProcessSignal != nil {
		return d.cfg.testProcessSignal(pid, sig)
	}
	return syscall.Kill(pid, sig)
}

// inspectForStop reads a process and decides if it may be stopped. Everything
// it can read goes into the outcome, for a refused process too.
func (d *Daemon) inspectForStop(pid int) *stopTarget {
	t := &stopTarget{out: protocol.ProcessOutcome{PID: pid}}
	if reason := staticProcessRefusal(pid); reason != "" {
		t.out.Outcome, t.out.Reason = protocol.ProcessRefused, reason
		if pid > 0 {
			if v, err := readProcView(pid); err == nil {
				t.fill(v)
			}
		}
		return t
	}
	v, err := readProcView(pid)
	if err != nil {
		switch probe := syscall.Kill(pid, 0); { // signal 0 only checks that the process exists
		case errors.Is(probe, syscall.ESRCH):
			t.out.Outcome, t.out.Reason = protocol.ProcessNotFound, "no such process"
		case errors.Is(probe, syscall.EPERM):
			t.out.Outcome, t.out.Reason = protocol.ProcessRefused, "it belongs to another user, so the daemon's user may not signal it"
		default:
			t.out.Outcome, t.out.Reason = protocol.ProcessRefused, "its details cannot be read: "+err.Error()
		}
		return t
	}
	t.fill(v)
	if reason := d.processRefusal(v); reason != "" {
		t.out.Outcome, t.out.Reason = protocol.ProcessRefused, reason
		return t
	}
	if v.Zombie {
		t.out.Outcome, t.out.Reason = protocol.ProcessNotFound, "it has already ended; only its parent can remove the zombie entry"
		return t
	}
	t.allowed = true
	return t
}

func (t *stopTarget) fill(v procView) {
	t.token = v.Token
	t.out.Command = clipText(cleanLine(v.Command), processCommandMax)
	t.out.ParentPID = v.PPID
	if !v.Started.IsZero() {
		t.out.Started = v.Started.UTC().Format(time.RFC3339)
	}
}

// sameRun tells if the process is still alive and is the run that was read, not
// a later process that reuses the number.
func (t *stopTarget) sameRun() bool {
	token, alive := procToken(t.out.PID)
	return alive && token == t.token
}

// waitProcessesGone waits until every target has ended, or limit passes.
func waitProcessesGone(ctx context.Context, targets []*stopTarget, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for {
		running := false
		for _, t := range targets {
			if t.sameRun() {
				running = true
				break
			}
		}
		if !running || !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(processPoll):
		}
	}
}

// staticProcessRefusal refuses what needs no look at the process: a number that
// is not a process, the init process, and the daemon itself.
func staticProcessRefusal(pid int) string {
	switch {
	case pid <= 0:
		return "not a valid process ID"
	case pid == 1:
		return "pid 1 is the system init process"
	case pid == os.Getpid():
		return "this is the agent-net daemon"
	}
	return ""
}

// procLookup reads a process by its ID. A process stop reads live (readProcView).
// The process list reads from one snapshot taken at the start of the call.
type procLookup func(pid int) (procView, error)

// processRefusal says why a process that exists must not be stopped, or returns
// "". It reads the roster and the process table live: a process stop checks
// identity again right before it signals.
func (d *Daemon) processRefusal(v procView) string {
	return d.processRefusalWith(v, d.readClaudeRoster(time.Now()), readProcView)
}

// processRefusalWith is processRefusal with the roster and the process reader
// given. The rules (decision 0102): a process of another user, the agent-net
// daemon or gateway, and a live agent-net session or worker. Also (decision
// 0102, Claude roster): a live Claude Code background session, the Claude
// supervisor, and any process inside a live session.
func (d *Daemon) processRefusalWith(v procView, roster *claudeRoster, lookup procLookup) string {
	if reason := staticProcessRefusal(v.PID); reason != "" {
		return reason
	}
	if me := os.Getuid(); v.UID != me || v.EUID != me {
		return fmt.Sprintf("it belongs to another user (uid %d): only processes of the daemon's own user may be stopped", v.UID)
	}
	if isAgentNetService(v.Command) {
		return "this is the agent-net daemon or gateway"
	}
	if reason := d.knownAgentNetProcess(v.PID); reason != "" {
		return reason
	}
	return claudeRosterProcessRefusal(v, roster, lookup)
}

// isAgentNetService tells if a command line is the agent-net daemon or gateway:
// the agent-net program with the subcommand daemon or gateway. Other
// subcommands, such as the MCP shim of a session, are not services.
func isAgentNetService(command string) bool {
	f := strings.Fields(command)
	if len(f) < 2 || filepath.Base(f[0]) != "agent-net" {
		return false
	}
	return f[1] == "daemon" || f[1] == "gateway"
}

// knownAgentNetProcess returns the reason for refusing pid when it is a live
// session or worker known to this daemon, or "".
func (d *Daemon) knownAgentNetProcess(pid int) string {
	d.mu.Lock()
	for _, ls := range d.sessions {
		if ls.info.PID == pid {
			id := firstNonEmpty(ls.sessionID, ls.info.NativeID)
			reason := fmt.Sprintf("it is a live agent-net session (%s %s)", ls.info.Harness, id)
			d.mu.Unlock()
			return reason
		}
	}
	d.mu.Unlock()
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for id, w := range d.workers {
		if w.pid == pid {
			return fmt.Sprintf("it is a live agent-net worker (%s)", id)
		}
	}
	return ""
}

func clipText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

// processOutcomeLine is one process of a result, as one line of the Inbox
// notice and the log.
func processOutcomeLine(p protocol.ProcessOutcome) string {
	what := p.Outcome
	switch p.Outcome {
	case protocol.ProcessStoppedTerm:
		what = "stopped by TERM"
	case protocol.ProcessStoppedKill:
		what = "stopped by KILL"
	case protocol.ProcessNotFound:
		what = "not found"
	}
	line := fmt.Sprintf("pid %d %s", p.PID, what)
	if p.Reason != "" {
		line += " (" + p.Reason + ")"
	}
	if p.Command != "" {
		line += ": " + p.Command
		details := fmt.Sprintf("ppid %d", p.ParentPID)
		if p.Started != "" {
			details += ", started " + p.Started
		}
		line += " (" + details + ")"
	}
	return line
}
