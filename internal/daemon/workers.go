package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Workers (decision 0002, 0008). This daemon starts the harness as its own
// child process, in a new process group, in the project folder. It owns the
// process: it keeps the process ID and start time in daemon.db, kills the
// whole group when the worker ends, and takes its workers back after a
// restart of the daemon (or cleans up after the ones that died meanwhile).

// WorkerSettings are the limits and timeouts. Zero fields take the defaults
// of decision 0002.
type WorkerSettings struct {
	// Max is how many workers may run on this machine at once.
	Max int
	// Idle ends a worker with no message to or from it for this long.
	Idle time.Duration
	// MaxAge ends a worker this long after it started.
	MaxAge time.Duration
	// OwnerGrace ends a worker whose owner session ended or stayed offline
	// for this long.
	OwnerGrace time.Duration
	// Tick is how often the limits are checked. Default 5 s.
	Tick time.Duration
	// StartTimeout is how long a start may take. Default 45 s.
	StartTimeout time.Duration
	// KillGrace is how long a worker gets after SIGTERM before SIGKILL.
	// Default 3 s.
	KillGrace time.Duration
	// MCPSettle is how long a Claude worker waits after its MCP server said
	// hello before it gets the task. Claude Code connects its MCP servers in
	// the background, and a turn that starts before they are ready has none of
	// their tools. Default 3 s; negative means no wait.
	MCPSettle time.Duration
}

func (s WorkerSettings) filled() WorkerSettings {
	if s.Max <= 0 {
		s.Max = protocol.DefaultWorkerMax
	}
	if s.Idle <= 0 {
		s.Idle = protocol.DefaultWorkerIdle
	}
	if s.MaxAge <= 0 {
		s.MaxAge = protocol.DefaultWorkerMaxAge
	}
	if s.OwnerGrace <= 0 {
		s.OwnerGrace = protocol.DefaultWorkerOwnerGrace
	}
	if s.Tick <= 0 {
		s.Tick = 5 * time.Second
	}
	if s.StartTimeout <= 0 {
		s.StartTimeout = 45 * time.Second
	}
	if s.KillGrace <= 0 {
		s.KillGrace = 3 * time.Second
	}
	if s.MCPSettle == 0 {
		s.MCPSettle = 3 * time.Second
	}
	return s
}

// worker is one running worker. Fields are guarded by Daemon.wmu.
type worker struct {
	id, harness, project, path, model, owner string
	nativeID, sessionID                      string
	pid, pgid                                int
	token                                    string
	startedAt, lastActive                    time.Time
	savedAt                                  time.Time // when lastActive was last written
	ownerGoneSince                           time.Time
	ownerChecked                             time.Time
	stopping                                 bool
	dir                                      string
	logPath                                  string
	// Worktree isolation (W13).
	mainPath, worktree, branch, base string
	// taskMsg is the last request from the owner; doneStatus the status of the
	// last hand-back ("" if none); taskText is the short line of that request.
	taskMsg, doneStatus, taskText string
	// apiBase and apiPass reach the harness's own server, for its statistics.
	// They are kept in memory only.
	apiBase, apiPass string
	// discardStdout drops the standard output of the process: Pi's rpc mode
	// writes its whole event stream there, which nobody reads.
	discardStdout bool
	// ref is the harness's own handle for the worker: the background session
	// ID of a Claude worker, the address of a Codex app server. It is kept in
	// the database, so an adopted worker can be found again.
	ref string
	// info is the worker part of the session, made once so that a comparison
	// of session infos does not see a change each time.
	info *protocol.WorkerInfo
	// codex is the connection to the worker's Codex app server.
	codex     *codexConn
	exited    chan struct{} // closed when an own child is reaped; nil if adopted
	ready     chan struct{} // closed when the session has a gateway ID
	readyOnce sync.Once
}

// launcher starts one kind of harness for a worker.
type launcher interface {
	// start runs the harness for w and returns once it is a session known to
	// the gateway (w.ready is closed). It calls d.spawnWorker for the process.
	start(ctx context.Context, d *Daemon, w *worker) error
}

// stopper is a launcher that has a polite way to stop its harness: it is tried
// before the process group is killed (for Claude, `claude stop`).
type stopper interface {
	stop(d *Daemon, w *worker)
}

// adopter is a launcher that must reconnect to a worker that survived a
// restart of the daemon.
type adopter interface {
	adopt(ctx context.Context, d *Daemon, w *worker) error
}

// ender is a launcher that has more to clean up when a worker ends.
type ender interface {
	end(d *Daemon, w *worker)
}

func (d *Daemon) workerInfo(w *worker) *protocol.WorkerInfo {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if w.info == nil {
		w.info = &protocol.WorkerInfo{ID: w.id, Owner: w.owner, Project: w.project, StartedAt: w.startedAt.Unix()}
	}
	return w.info
}

// workerByNative returns the worker that has this harness session, or nil.
func (d *Daemon) workerByNative(harness, native string) *worker {
	if native == "" {
		return nil
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.harness == harness && w.nativeID == native {
			return w
		}
	}
	return nil
}

// attachWorkerProcess records a process that the daemon did not start as the
// process of w: a Claude background session belongs to Claude's own service, not
// to this daemon. The group is the session's own (Claude makes each one a group
// leader). The caller has checked that pid is the session.
func (d *Daemon) attachWorkerProcess(w *worker, pid int) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}
	tok, _ := procToken(pid)
	d.wmu.Lock()
	w.pid, w.pgid, w.token = pid, pgid, tok
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
}

// ---- finding workers ----

// workerForPeer returns the worker whose process group the process is in.
func (d *Daemon) workerForPeer(pid int) *worker {
	if pid <= 0 {
		return nil
	}
	pgid, err := syscall.Getpgid(pid)
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.pid != 0 && (w.pid == pid || (err == nil && w.pgid == pgid)) {
			return w
		}
	}
	return nil
}

// workerOwnsNative tells whether a worker already has this harness session.
func (d *Daemon) workerOwnsNative(harness, native string) bool {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.harness == harness && w.nativeID == native {
			return true
		}
	}
	return false
}

// bindWorkerNative ties a worker to its one harness session. A worker has one
// session: a second one (for example another session that the harness
// registers) is refused.
func (d *Daemon) bindWorkerNative(w *worker, native string) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if w.nativeID == "" {
		w.nativeID = native
		d.saveWorkerLocked(w)
		return nil
	}
	if w.nativeID != native {
		return fmt.Errorf("worker %s already has session %s: a worker has one session", w.id, w.nativeID)
	}
	return nil
}

// workerSessionReady records the gateway ID of a worker's session.
func (d *Daemon) workerSessionReady(w *worker, sessionID string) {
	if sessionID == "" {
		return
	}
	d.wmu.Lock()
	w.sessionID = sessionID
	w.lastActive = time.Now() // the idle clock starts when the session exists
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
	w.readyOnce.Do(func() { close(w.ready) })
}

func (d *Daemon) workerBySession(sessionID string) *worker {
	if sessionID == "" {
		return nil
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.sessionID == sessionID {
			return w
		}
	}
	return nil
}

// touchWorker records a message to or from a worker session.
func (d *Daemon) touchWorker(sessionIDs ...string) {
	for _, id := range sessionIDs {
		w := d.workerBySession(id)
		if w == nil {
			continue
		}
		now := time.Now()
		d.wmu.Lock()
		w.lastActive = now
		if now.Sub(w.savedAt) > 15*time.Second {
			d.saveWorkerLocked(w)
		}
		d.wmu.Unlock()
	}
}

func (d *Daemon) saveWorkerLocked(w *worker) {
	w.savedAt = time.Now()
	if w.pid == 0 {
		return // still starting
	}
	if err := d.st.saveWorker(d.rowLocked(w, "")); err != nil {
		d.Logf("worker %s: save: %v", w.id, err)
	}
}

// rowLocked is the database row of a worker. Callers hold d.wmu.
func (d *Daemon) rowLocked(w *worker, ended string) workerRow {
	return workerRow{ID: w.id, Harness: w.harness, Project: w.project, Path: w.path, Model: w.model,
		Owner: w.owner, NativeID: w.nativeID, SessionID: w.sessionID, PID: w.pid, PGID: w.pgid, StartToken: w.token,
		StartedAt: w.startedAt, LastActive: w.lastActive, EndedReason: ended, MainPath: w.mainPath, Worktree: w.worktree,
		Branch: w.branch, Base: w.base, TaskMsg: w.taskMsg, TaskText: w.taskText, DoneStatus: w.doneStatus, Ref: w.ref}
}

// ---- starting ----

// startWorker handles "worker.launch" from the gateway.
func (d *Daemon) startWorker(ctx context.Context, l protocol.WorkerLaunch) (out protocol.WorkerLaunched, err error) {
	set := d.cfg.Workers.filled()
	refuse := func(format string, a ...any) (protocol.WorkerLaunched, error) {
		err := fmt.Errorf(format, a...)
		d.Logf("worker.refused project=%s harness=%s owner=%s reason=%q", l.Project, l.Harness, l.Owner, err.Error())
		return protocol.WorkerLaunched{}, err
	}
	if d.cfg.NoLaunch {
		return refuse("this machine does not accept workers (it was set up with --no-launch)")
	}
	ln := d.launchers[l.Harness]
	if ln == nil {
		return refuse("this daemon cannot start %q workers", l.Harness)
	}
	if fi, err := os.Stat(l.Path); err != nil || !fi.IsDir() {
		return refuse("the project folder %s is not there", l.Path)
	}
	if !protocol.ValidIsolation(l.Isolation) {
		return refuse("isolation can be %q, %q or empty, not %q", protocol.IsolationWorktree, protocol.IsolationNone, l.Isolation)
	}
	// The default is a worktree when the project is a git repository (W17).
	if l.Isolation == "" && isGitRepo(ctx, l.Path) {
		l.Isolation = protocol.IsolationWorktree
	}
	if l.Harness == "pi" && l.Isolation != protocol.IsolationWorktree {
		return refuse("a Pi worker needs isolation %q: Pi has no permission prompts, so it must not work in your own checkout", protocol.IsolationWorktree)
	}
	if l.Owner == "" {
		return refuse("a worker needs an owner")
	}
	w := &worker{id: protocol.NewID("w"), harness: l.Harness, project: l.Project, path: l.Path, model: l.Model, owner: l.Owner,
		startedAt: time.Now(), ready: make(chan struct{})}
	w.lastActive = w.startedAt
	d.wmu.Lock()
	if len(d.workers) >= set.Max {
		n := len(d.workers)
		d.wmu.Unlock()
		return refuse("this machine already runs %d workers, which is the limit", n)
	}
	d.workers[w.id] = w
	d.wmu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, set.StartTimeout)
	defer cancel()
	var err2 error
	if l.Isolation == protocol.IsolationWorktree {
		err2 = d.createWorktree(cctx, w)
	}
	if err2 == nil {
		err2 = ln.start(cctx, d, w)
	}
	if err := err2; err != nil {
		tail := d.workerLogTail(w)
		d.endWorker(w, protocol.StopStartFails)
		d.Logf("worker.refused project=%s harness=%s owner=%s reason=%q", l.Project, l.Harness, l.Owner, err.Error())
		if tail != "" {
			err = fmt.Errorf("%w (harness output: %s)", err, tail)
		}
		return protocol.WorkerLaunched{}, err
	}
	d.wmu.Lock()
	sid, wt, br := w.sessionID, w.worktree, w.branch
	d.wmu.Unlock()
	d.Logf("worker.started worker=%s session=%s project=%s path=%s harness=%s model=%s owner=%s pid=%d worktree=%s branch=%s",
		w.id, sid, w.project, w.path, w.harness, w.model, w.owner, w.pid, wt, br)
	return protocol.WorkerLaunched{SessionID: sid, WorkerID: w.id, Worktree: wt, Branch: br}, nil
}

// spawnWorker starts the process for w in its own process group, in the project
// folder, with output going to a private log file. It records the process ID
// and start time. The caller adds the harness-specific arguments and
// environment.
func (d *Daemon) spawnWorker(w *worker, bin string, args []string, env []string) error {
	return d.spawnWorkerStdin(w, bin, args, env, nil)
}

// spawnWorkerStdin is spawnWorker with a file for the standard input of the
// process (nil is /dev/null).
func (d *Daemon) spawnWorkerStdin(w *worker, bin string, args []string, env []string, stdin *os.File) error {
	dir := filepath.Join(d.cfg.StateDir, "workers", w.id)
	logPath := filepath.Join(dir, "worker.log")
	d.wmu.Lock() // endWorker and workerLogTail read these from other goroutines
	w.dir, w.logPath = dir, logPath
	d.wmu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, args...)
	cmd.Dir = w.path
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if w.discardStdout {
		cmd.Stdout = nil // /dev/null
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot run %s: %w", bin, err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	d.wmu.Lock()
	w.pid, w.pgid, w.exited = cmd.Process.Pid, cmd.Process.Pid, exited
	d.wmu.Unlock()
	for i := 0; i < 20; i++ {
		if tok, _ := procToken(w.pid); tok != "" {
			d.wmu.Lock() // gone() reads the token from the worker loop
			w.token = tok
			d.wmu.Unlock()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.wmu.Lock()
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
	return nil
}

// setWorkerDir records the private folder and log path of w, under the lock
// that endWorker and workerLogTail read them with, and returns the folder.
func (d *Daemon) setWorkerDir(w *worker) string {
	dir := filepath.Join(d.cfg.StateDir, "workers", w.id)
	d.wmu.Lock()
	w.dir, w.logPath = dir, filepath.Join(dir, "worker.log")
	d.wmu.Unlock()
	return dir
}

func (d *Daemon) workerLogTail(w *worker) string {
	d.wmu.Lock()
	logPath := w.logPath
	d.wmu.Unlock()
	if logPath == "" {
		return ""
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 400 {
		s = "..." + s[len(s)-400:]
	}
	return strings.Join(strings.Fields(s), " ")
}

// ---- ending ----

// gone tells whether the worker's process has ended.
func (d *Daemon) gone(w *worker) bool {
	d.wmu.Lock()
	exited, pid, token := w.exited, w.pid, w.token
	d.wmu.Unlock()
	if pid == 0 {
		return false
	}
	if exited != nil {
		select {
		case <-exited:
			return true
		default:
		}
	}
	tok, alive := procToken(pid)
	return !alive || (token != "" && tok != token)
}

// killGroup stops the process group: SIGTERM, a short wait, then SIGKILL.
func (d *Daemon) killGroup(w *worker) {
	d.wmu.Lock()
	pgid, token := w.pgid, w.token
	d.wmu.Unlock()
	if pgid <= 0 {
		return
	}
	if tok, alive := procToken(pgid); !alive || (token != "" && tok != token) {
		// The leader is gone. Stragglers in the group get SIGKILL below, but
		// only if the group still exists.
		syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(d.cfg.Workers.filled().KillGrace)
	for time.Now().Before(deadline) && !d.gone(w) {
		time.Sleep(25 * time.Millisecond)
	}
	syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 40 && !d.gone(w); i++ {
		time.Sleep(25 * time.Millisecond)
	}
}

// endWorker stops a worker and forgets it. It is safe to call twice.
func (d *Daemon) endWorker(w *worker, reason string) {
	d.wmu.Lock()
	if w.stopping {
		d.wmu.Unlock()
		return
	}
	w.stopping = true
	sid, harness, project, id, dir, started := w.sessionID, w.harness, w.project, w.id, w.dir, w.startedAt
	done := w.doneStatus != ""
	d.wmu.Unlock()
	// A worker that ends with no hand-back still tells its owner (W11). The
	// harness statistics need the process, so they are read before the kill.
	status, note := d.noticeFor(reason)
	sendNotice := !done && status != "" && sid != ""
	var stats workerStats
	if sendNotice {
		stats = d.workerStats(w)
	}
	if st, ok := d.launchers[w.harness].(stopper); ok && w.pid != 0 {
		st.stop(d, w)
	}
	d.killGroup(w)
	if en, ok := d.launchers[w.harness].(ender); ok {
		en.end(d, w)
	}
	d.wmu.Lock()
	delete(d.workers, w.id)
	row := d.rowLocked(w, reason)
	d.wmu.Unlock()
	endStatus, endNote := d.endFacts(w, reason)
	d.rememberEnded(w, endStatus, endNote, time.Now())
	if sendNotice {
		if _, err := d.sendNotice(context.Background(), w, d.notice(w, status, note, stats), ""); err != nil {
			d.Logf("worker %s: notice: %v", id, err)
		}
	}
	d.finishWorktree(w)
	if w.pid != 0 {
		// Kept until the gateway has been told.
		if err := d.st.saveWorker(row); err != nil {
			d.Logf("worker %s: save: %v", id, err)
		}
	} else {
		d.st.deleteWorker(id)
	}
	if dir != "" {
		os.RemoveAll(dir)
	}
	d.Logf("worker.stopped worker=%s session=%s project=%s harness=%s reason=%s ran=%s", id, sid, project, harness, reason,
		time.Since(started).Round(time.Second))
	d.reportWorkerEnds(context.Background())
}

// reportWorkerEnds tells the gateway about workers that ended, once each.
func (d *Daemon) reportWorkerEnds(ctx context.Context) {
	rows, err := d.st.workers()
	if err != nil {
		return
	}
	lostNoticed := map[string]bool{}
	for _, r := range rows {
		if r.EndedReason == "" {
			continue
		}
		if r.SessionID == "" {
			d.st.deleteWorker(r.ID) // it never became a session: the gateway has nothing to update
			continue
		}
		if r.EndedReason == protocol.StopLost && !lostNoticed[r.ID] {
			lostNoticed[r.ID] = true
			d.lostNotice(ctx, r)
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := d.call(cctx, "worker.stopped", protocol.WorkerStopped{SessionID: r.SessionID, WorkerID: r.ID,
			Project: r.Project, Harness: r.Harness, Reason: r.EndedReason})
		cancel()
		var re RemoteError
		switch {
		case err == nil, errors.As(err, &re):
			// Told, or refused for good (an older gateway, an unknown session).
			d.st.deleteWorker(r.ID)
		default:
			return // not connected: try again at the next connect
		}
	}
}

// stopWorkerSession handles "worker.kill" from the gateway.
func (d *Daemon) stopWorkerSession(sessionID string) error {
	w := d.workerBySession(sessionID)
	if w == nil {
		return fmt.Errorf("no worker with session %s runs on this machine", sessionID)
	}
	d.endWorker(w, protocol.StopRequested)
	return nil
}

// ---- keeping an eye on workers ----

func (d *Daemon) workerLoop(ctx context.Context) {
	set := d.cfg.Workers.filled()
	t := time.NewTicker(set.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.checkWorkers(ctx)
		}
	}
}

func (d *Daemon) checkWorkers(ctx context.Context) {
	set := d.cfg.Workers.filled()
	d.wmu.Lock()
	list := make([]*worker, 0, len(d.workers))
	for _, w := range d.workers {
		// A worker that is still starting belongs to its start, which has its
		// own time limit and reads the harness output when it fails.
		started := false
		select {
		case <-w.ready:
			started = true
		default:
		}
		if w.pid != 0 && !w.stopping && started {
			list = append(list, w)
		}
	}
	d.wmu.Unlock()
	now := time.Now()
	for _, w := range list {
		if ctx.Err() != nil {
			return
		}
		d.wmu.Lock()
		age, idle, owner := now.Sub(w.startedAt), now.Sub(w.lastActive), w.owner
		// A worker that is still starting is not idle. Until its first request
		// is noted, it also gets the start timeout on top of the idle limit, so
		// the task can arrive and a notice always has a request to answer.
		idleLimit := set.Idle
		if w.sessionID == "" {
			idleLimit = -1
		} else if w.taskMsg == "" {
			idleLimit += set.StartTimeout
		}
		due := now.Sub(w.ownerChecked) >= set.Tick*4
		d.wmu.Unlock()
		switch {
		case d.gone(w):
			d.endWorker(w, protocol.StopExited)
		case age >= set.MaxAge:
			d.endWorker(w, protocol.StopMaxAge)
		case idleLimit >= 0 && idle >= idleLimit:
			d.endWorker(w, protocol.StopIdle)
		case due && d.Connected():
			offline := d.ownerOffline(ctx, owner)
			d.wmu.Lock()
			w.ownerChecked = now
			if !offline {
				w.ownerGoneSince = time.Time{}
			} else if w.ownerGoneSince.IsZero() {
				w.ownerGoneSince = now
			}
			gone := !w.ownerGoneSince.IsZero() && now.Sub(w.ownerGoneSince) >= set.OwnerGrace
			d.wmu.Unlock()
			if gone {
				d.endWorker(w, protocol.StopOwnerGone)
			}
		}
	}
}

// ownerOffline asks the gateway whether the owner session is offline or gone.
// A gateway that cannot be reached does not count: nobody can tell.
func (d *Daemon) ownerOffline(ctx context.Context, owner string) bool {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	data, err := d.call(cctx, "query.agent", protocol.QueryAgent{SessionID: owner})
	if err != nil {
		var re RemoteError
		return errors.As(err, &re) // "unknown agent": the session is gone
	}
	var r struct {
		Agent protocol.AgentView `json:"agent"`
	}
	if json.Unmarshal(data, &r) != nil {
		return false
	}
	return !r.Agent.Online
}

// recentWindow is how long an ended worker stays visible to `workers -recent`.
const recentWindow = 2 * time.Minute

// recentWorker is a worker that ended, kept for a short while so a status view
// can show the end it reached.
type recentWorker struct {
	view protocol.WorkerView
	at   time.Time
}

// rememberEnded keeps the facts of a worker that just ended. A worker that
// never became a session is not kept.
func (d *Daemon) rememberEnded(w *worker, status, note string, at time.Time) {
	d.wmu.Lock()
	sid, owner, harness, model, project, task := w.sessionID, w.owner, w.harness, w.model, w.project, w.taskText
	started := w.startedAt
	d.wmu.Unlock()
	if sid == "" {
		return
	}
	view := protocol.WorkerView{SessionID: sid, NodeID: d.cfg.NodeID, Hostname: d.cfg.Hostname, Harness: harness,
		Model: model, Project: project, Owner: owner, StartedAt: started.Unix(), Task: task, EndedAt: at.Unix(),
		EndStatus: status, EndNote: note, Status: status}
	d.rmu.Lock()
	if d.recent == nil {
		d.recent = map[string]recentWorker{}
	}
	d.recent[sid] = recentWorker{view: view, at: at}
	d.rmu.Unlock()
}

// recentWorkers returns the workers that ended in the last two minutes, the
// newest first, and forgets the older ones.
func (d *Daemon) recentWorkers(now time.Time) []protocol.WorkerView {
	d.rmu.Lock()
	defer d.rmu.Unlock()
	out := []protocol.WorkerView{}
	for sid, r := range d.recent {
		if now.Sub(r.at) > recentWindow {
			delete(d.recent, sid)
			continue
		}
		out = append(out, r.view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EndedAt > out[j].EndedAt })
	return out
}

// workerTask is the short task line of the worker with this session, "" when
// this daemon did not start it.
func (d *Daemon) workerTask(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.sessionID == sessionID {
			return w.taskText
		}
	}
	return ""
}

// endFacts is the status and note a status view shows for an ended worker.
// It follows the notice: a hand-back gives its own status, and a worker that
// ended on its own gets the reason the notice uses.
func (d *Daemon) endFacts(w *worker, reason string) (status, note string) {
	d.wmu.Lock()
	done := w.doneStatus
	d.wmu.Unlock()
	if done != "" {
		return done, ""
	}
	status, note = d.noticeFor(reason)
	if status == "" {
		status = protocol.StatusStopped
		if reason == protocol.StopOwnerGone {
			note = "its owner ended or stayed offline"
		}
	}
	return status, note
}

// ---- after a restart ----

// adoptWorkers takes back the workers that were running when the daemon last
// stopped. A worker whose process is still the same run (same process ID and
// start time) is adopted. Any other is ended and reported as lost.
func (d *Daemon) adoptWorkers() {
	rows, err := d.st.workers()
	if err != nil {
		d.Logf("workers: %v", err)
		return
	}
	for _, r := range rows {
		if r.EndedReason != "" {
			continue // reported at the next connect
		}
		tok, alive := procToken(r.PID)
		if !alive || tok != r.StartToken {
			r.EndedReason = protocol.StopLost
			d.st.saveWorker(r)
			os.RemoveAll(filepath.Join(d.cfg.StateDir, "workers", r.ID))
			d.Logf("worker.stopped worker=%s session=%s project=%s harness=%s reason=%s", r.ID, r.SessionID, r.Project, r.Harness, protocol.StopLost)
			continue
		}
		w := &worker{id: r.ID, harness: r.Harness, project: r.Project, path: r.Path, model: r.Model, owner: r.Owner,
			nativeID: r.NativeID, sessionID: r.SessionID, pid: r.PID, pgid: r.PGID, token: r.StartToken,
			startedAt: r.StartedAt, lastActive: time.Now(), ready: make(chan struct{}),
			dir: filepath.Join(d.cfg.StateDir, "workers", r.ID), mainPath: r.MainPath, worktree: r.Worktree,
			branch: r.Branch, base: r.Base, taskMsg: r.TaskMsg, taskText: r.TaskText, doneStatus: r.DoneStatus, ref: r.Ref}
		w.logPath = filepath.Join(w.dir, "worker.log")
		w.readyOnce.Do(func() { close(w.ready) })
		d.wmu.Lock()
		d.workers[w.id] = w
		d.wmu.Unlock()
		if ad, ok := d.launchers[w.harness].(adopter); ok {
			actx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := ad.adopt(actx, d, w)
			cancel()
			if err != nil {
				// It runs, but the daemon cannot talk to it any more. End it.
				d.Logf("worker %s: cannot be taken back: %v", w.id, err)
				go d.endWorker(w, protocol.StopLost)
				continue
			}
		}
		d.Logf("worker.adopted worker=%s session=%s project=%s pid=%d", w.id, w.sessionID, w.project, w.pid)
	}
}

// workerCount is how many workers run on this machine.
func (d *Daemon) workerCount() int {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return len(d.workers)
}
