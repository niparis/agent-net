package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A stop is final. The stopped session is kept in worker_no_restart, so no
// message, queued restart or move to another model starts it again. A
// continue makes a new worker and is the one way to run the task again; the
// worker it continues is kept there too, so a message to the old session does
// not run a second copy next to the new one.

const (
	noRestartStopped   = "stopped"
	noRestartContinued = "continued"
)

func (s *state) markNoRestart(sessionID, why string) error {
	if sessionID == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO worker_no_restart(session_id, why, at) VALUES(?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET why=CASE WHEN worker_no_restart.why=? THEN worker_no_restart.why ELSE excluded.why END`,
		sessionID, why, time.Now().UnixNano(), noRestartStopped)
	return err
}

// noRestart says why a worker session may not run again, or "".
func (s *state) noRestart(sessionID string) string {
	var why string
	_ = s.db.QueryRow(`SELECT why FROM worker_no_restart WHERE session_id=?`, sessionID).Scan(&why)
	return why
}

// noRestartReason is the refusal for a worker session that may not run again.
func noRestartReason(sessionID, why string) string {
	switch why {
	case noRestartStopped:
		return fmt.Sprintf("worker %s was stopped, so it does not start again; continue it with agent_worker_continue to run its task again", sessionID)
	case noRestartContinued:
		return fmt.Sprintf("worker %s was continued as a new worker, so it does not start again; send your message to the new worker", sessionID)
	}
	return ""
}

// resumeRefusal says why an ended worker cannot start again for a message,
// or "". Both restart paths, at once and from the queue, ask it.
func (d *Daemon) resumeRefusal(row workerRow) string {
	if why := d.st.noRestart(row.SessionID); why != "" {
		return noRestartReason(row.SessionID, why)
	}
	if row.EndedReason != protocol.StopHandback || !row.Resumable {
		return fmt.Sprintf("worker %s ended with reason %s, so it cannot be started again: only a worker that ended after its hand-back can", row.SessionID, row.EndedReason)
	}
	return ""
}

// claimRelaunch takes the right to start a new run from worker id: a resume
// of its session, or a continue into a new worker. A second claim is refused
// until release is called, so a message and a continue cannot run two copies.
func (d *Daemon) claimRelaunch(id, session, kind string) (release func(), err error) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if other, busy := d.relaunching[id]; busy {
		if other == noRestartContinued {
			return nil, fmt.Errorf("worker %s is being continued as a new worker now; send your message to the new worker", session)
		}
		return nil, fmt.Errorf("worker %s is starting again for a message now; wait for it to end, or stop it first", session)
	}
	if d.relaunching == nil {
		d.relaunching = make(map[string]string)
	}
	d.relaunching[id] = kind
	return func() {
		d.wmu.Lock()
		delete(d.relaunching, id)
		d.wmu.Unlock()
	}, nil
}

// stopWorkerSession handles "worker.kill" from the gateway. A queued start
// (a q_ ID) is cancelled instead. A stop of a session this machine no longer
// runs still marks it stopped and ends what is left of it, and answers ok.
func (d *Daemon) stopWorkerSession(k protocol.WorkerKill, validate ...bool) error {
	sessionID := k.Session
	if protocol.IsQueueID(sessionID) {
		return d.cancelQueued(sessionID, k.Requester, k.AsOwner, validate...)
	}
	if len(validate) > 0 && validate[0] {
		return nil
	}
	if err := d.st.markNoRestart(sessionID, noRestartStopped); err != nil {
		return err
	}
	// A restart waiting in the queue never runs.
	if d.st.removeQueuedForSession(sessionID) {
		d.Logf("worker.queue_cancelled session=%s by=%s reason=stopped", sessionID, k.Requester)
	}
	if w := d.workerBySession(sessionID); w != nil {
		d.endWorker(w, protocol.StopRequested)
	}
	row, ok, err := d.st.workerBySession(sessionID)
	if err != nil || !ok {
		return err
	}
	if row.Resumable {
		row.Resumable = false
		if err := d.st.saveWorker(row); err != nil {
			d.Logf("worker %s: save stop: %v", row.ID, err)
		}
	}
	if n := d.killStoppedSessionProcesses(row); n > 0 {
		d.Logf("worker.stop_processes session=%s harness=%s ended=%d", sessionID, row.Harness, n)
	}
	return nil
}

// killStoppedSessionProcesses ends the processes left from a stopped worker's
// session: a view of its conversation (agy --conversation <id>) or a program
// still running in its worktree. Only processes of this user that this daemon
// or agent-net's own herdr server started are touched; a live worker's
// process is never one of them. It returns how many it ended.
func (d *Daemon) killStoppedSessionProcesses(row workerRow) int {
	self, uid := os.Getpid(), os.Getuid()
	worktree := ""
	if row.Worktree != "" {
		worktree = filepath.Clean(row.Worktree)
		if resolved, err := filepath.EvalSymlinks(worktree); err == nil {
			worktree = resolved
		}
	}
	type target struct {
		pid   int
		token string
	}
	var pids []int
	for _, pid := range processList() {
		if pid > 1 && pid != self {
			pids = append(pids, pid)
		}
	}
	// One read for every folder: on macOS each read is an lsof call.
	var cwds map[int]string
	if worktree != "" {
		cwds = processCWDs(pids)
	}
	var targets []target
	for _, pid := range pids {
		if !sessionLeftover(processArgs(pid), cwds[pid], row.NativeID, worktree) {
			continue
		}
		if v, err := readProcView(pid); err != nil || v.UID != uid || v.Zombie {
			continue
		}
		if w := d.workerForPeer(pid); w != nil {
			continue // a live worker's process, maybe the worker that continues this one
		}
		if !d.startedByAgentNet(pid, self) {
			continue
		}
		tok, live := procToken(pid)
		if !live || tok == "" {
			continue
		}
		targets = append(targets, target{pid, tok})
	}
	same := func(t target) bool { now, live := procToken(t.pid); return live && now == t.token }
	for _, t := range targets {
		_ = syscall.Kill(t.pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(d.workerSettings("").KillGrace)
	for time.Now().Before(deadline) {
		left := false
		for _, t := range targets {
			left = left || same(t)
		}
		if !left {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, t := range targets {
		if same(t) {
			_ = syscall.Kill(t.pid, syscall.SIGKILL)
		}
	}
	return len(targets)
}

// sessionLeftover says whether a process belongs to the stopped session: its
// command line names the session's native ID, or it runs in the worktree.
func sessionLeftover(args []string, cwd, native, worktree string) bool {
	if native != "" {
		for _, a := range args {
			if a == native || strings.HasSuffix(a, "="+native) {
				return true
			}
		}
	}
	if worktree == "" || cwd == "" {
		return false
	}
	cwd = filepath.Clean(cwd)
	return cwd == worktree || strings.HasPrefix(cwd, worktree+string(filepath.Separator))
}

// startedByAgentNet says whether pid descends from this daemon or from
// agent-net's own herdr server, where views asked from other machines run.
func (d *Daemon) startedByAgentNet(pid, self int) bool {
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if p == self {
			return true
		}
		if h, ok := herdrServerArgs(processArgs(p)); ok {
			return d.isAgentNetHerdr(h.bin, h.socket)
		}
		parent, err := processParentPID(p)
		if err != nil {
			return false
		}
		p = parent
	}
	return false
}

// workerStopResult is the caller's answer to a stop. A stop the worker's
// machine has not had yet is still a stop: the gateway marked the worker
// stopped. The answer says it is pending, with the gateway's note.
func workerStopResult(session string, raw json.RawMessage) protocol.WorkerStopResult {
	var a struct {
		Pending bool   `json:"pending"`
		Note    string `json:"note"`
	}
	_ = json.Unmarshal(raw, &a)
	return protocol.WorkerStopResult{Session: session, Stopped: true, Pending: a.Pending, Note: a.Note}
}
