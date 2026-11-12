package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// Workers (decision 0002, 0008). This daemon starts the harness as its own
// child process, in a new process group, in the project folder. It owns the
// process: it keeps the process ID and start time in daemon.db, kills the
// whole group when the worker ends, and takes its workers back after a
// restart of the daemon (or cleans up after the ones that died meanwhile).

// WorkerSettings are the limits and timeouts. Zero fields take the defaults
// of decision 0002.
type WorkerSettings struct {
	// Max is the hard cap of workers on this machine, a safety net behind the
	// load check (decision 0018). Default 12.
	Max int
	// CPUTarget is the 5-minute whole-machine CPU busy percent above which no
	// worker starts. Default 75.
	CPUTarget float64
	// MemReserve is the memory in bytes that stays free. Default: the larger
	// of 1.5 GB and 10 percent of RAM.
	MemReserve uint64
	// QueueTimeout is how long a start may wait in the queue. Default 2 h.
	QueueTimeout time.Duration
	// QueueTick is how often the queue is looked at. Default 15 s.
	QueueTick time.Duration
	// StartGap is the least time between two worker starts. Default 6 s;
	// zero or negative means none.
	StartGap time.Duration
	// Idle ends a worker with no message to or from it for this long.
	Idle time.Duration
	// IdleNotice is the wait after a turn ends without a hand-back.
	IdleNotice time.Duration
	// MaxAge ends a worker this long after it started.
	MaxAge time.Duration
	// OwnerGrace ends a worker whose owner session ended or stayed offline
	// for this long.
	OwnerGrace time.Duration
	// Tick is how often the limits are checked. Default 5 s.
	Tick time.Duration
	// StartTimeout is how long a start may take. Default 45 s.
	StartTimeout time.Duration
	// BackgroundStartTimeout bounds checkout and launch after the reply.
	BackgroundStartTimeout time.Duration
	// KillGrace is how long a worker gets after SIGTERM before SIGKILL.
	// Default 3 s.
	KillGrace time.Duration
	// MCPSettle is how long a Claude worker waits after its MCP server said
	// hello before it gets the task. Claude Code connects its MCP servers in
	// the background, and a turn that starts before they are ready has none of
	// their tools. Default 3 s; negative means no wait.
	MCPSettle time.Duration
	// Cleanup is how often ended worker records are cleaned: the local ones
	// that are past the longest run time, and the gateway's copy of them.
	// Default 10 minutes. Tests shorten it.
	Cleanup time.Duration
	// DeleteSessions controls harness data deletion at record expiry. Nil means on.
	DeleteSessions *bool
	// CacheMaxAge trims the shared worker Go build cache. Default 24 h.
	// 0 turns it off.
	CacheMaxAge time.Duration
	// CacheMaxBytes caps the combined size of the worker Go build cache and
	// the owner's ~/.cache/go-build. Default 15 GiB. Zero or negative turns
	// the size trim off.
	CacheMaxBytes int64
	// CacheLowBytes is the size the caches are trimmed down to when they are
	// over the cap. Default 10 GiB.
	CacheLowBytes int64
	// TmpMaxAge removes old subfolders from the worker test temp dir. Default 6 h.
	// 0 turns it off.
	TmpMaxAge time.Duration
	// OSSandbox lists the projects whose Pi and OpenCode workers run in the
	// OS sandbox ("*" is every project): workers.os_sandbox of node.json.
	OSSandbox []string
}

func (s WorkerSettings) filled() WorkerSettings {
	defaults := settings.Snapshot{}
	where := settings.Where{}
	number := func(key string, out any) {
		if e, err := defaults.Resolve(key, where); err == nil {
			_ = json.Unmarshal(e.Value, out)
		}
	}
	if s.Max <= 0 {
		number("workers.max_per_machine", &s.Max)
	}
	if s.CPUTarget <= 0 {
		number("workers.cpu_target", &s.CPUTarget)
	}
	if s.QueueTimeout <= 0 {
		s.QueueTimeout = defaults.Duration("workers.queue_timeout", where)
	}
	if s.QueueTick <= 0 {
		s.QueueTick = protocol.WorkerQueueTick
	}
	if s.StartGap == 0 {
		s.StartGap = defaults.Duration("workers.start_gap", where)
	}
	if s.Idle <= 0 {
		s.Idle = defaults.Duration("workers.idle_timeout", where)
	}
	if s.IdleNotice <= 0 {
		s.IdleNotice = 2 * time.Minute
	}
	if s.MaxAge <= 0 {
		s.MaxAge = defaults.Duration("workers.max_age", where)
	}
	if s.OwnerGrace <= 0 {
		s.OwnerGrace = defaults.Duration("workers.caller_gone_grace", where)
	}
	if s.Tick <= 0 {
		s.Tick = 5 * time.Second
	}
	if s.StartTimeout <= 0 {
		s.StartTimeout = 45 * time.Second
	}
	if s.BackgroundStartTimeout <= 0 {
		s.BackgroundStartTimeout = 10 * time.Minute
	}
	if s.KillGrace <= 0 {
		s.KillGrace = 3 * time.Second
	}
	if s.MCPSettle == 0 {
		s.MCPSettle = 3 * time.Second
	}
	if s.Cleanup <= 0 {
		s.Cleanup = 10 * time.Minute
	}
	if s.CacheMaxAge == 0 {
		s.CacheMaxAge = defaults.Duration("workers.cache_max_age", where)
	}
	if s.CacheMaxBytes == 0 {
		number("workers.cache_max_bytes", &s.CacheMaxBytes)
	}
	if s.CacheLowBytes == 0 {
		number("workers.cache_low_bytes", &s.CacheLowBytes)
	}
	if s.TmpMaxAge == 0 {
		s.TmpMaxAge = defaults.Duration("workers.tmp_max_age", where)
	}
	return s
}

// worker is one running worker. Fields are guarded by Daemon.wmu.
type worker struct {
	runGeneration                                      int
	id, startRef, harness, project, path, model, owner string
	// ownerName is the caller's name when the gateway knows it, for the
	// commit trailers.
	ownerName             string
	nativeID, sessionID   string
	pid, pgid             int
	token                 string
	startedAt, lastActive time.Time
	savedAt               time.Time // when lastActive was last written
	ownerGoneSince        time.Time
	ownerChecked          time.Time
	// sendsInFlight counts the messages to or from the worker that the daemon
	// has handed to the gateway but whose delivery has not been noted yet. A
	// round trip can take longer than the idle limit on a busy machine, so the
	// idle check must not end a worker while one of its messages is on the way.
	sendsInFlight int
	stopping      bool
	handingBack   bool // from the hand-back notice until the worker has ended
	// checking is true while the project's pre-hand-back check runs. The
	// hand-back ends the worker itself, so the tick must not race it.
	checking bool
	// endedCh is closed when the worker starts to end (endWorker). Made on first use.
	endedCh                           chan struct{}
	turnActive                        bool
	turnHandbackIdle                  bool
	turnAwaitingStart                 bool
	turnMessageID                     string // dequeued turn, retained until its start is observed
	turnQueue                         []protocol.Message
	idleNoticeSince                   time.Time
	idleNoticeSent, idleNoticePending bool
	idleNoticeGeneration              uint64
	idleNoticeReport, idleNoticeKey   string
	idleNoticeCancel                  context.CancelFunc
	// idleFollowUp is true when the owner's message starts a turn on a worker
	// that went idle without a hand-back. The OpenCode router clears the idle
	// notice before noteAsk runs, so noteAsk reads this to count the follow-up
	// as a continue.
	idleFollowUp bool
	dir          string
	logPath      string
	// Worktree isolation (W13).
	mainPath, worktree, branch, base string
	// mcpServers is the start's "approve" or "disable" for a Claude worker. It is
	// applied at the first launch and not kept: the choice lives in the folder.
	mcpServers string
	// taskMsg is the last request from the owner; doneStatus the status of the
	// last hand-back ("" if none); taskText is the short line of that request;
	// title is the worker's one-line title, empty when it is derived.
	taskMsg, doneStatus, taskText, title string
	// handbackReport is the last hand-back's report. At a hand-back end it is
	// the failure text of a failed run, so worker.stopped carries the provider
	// error a failed Codex turn reports (decision 0145). It is not persisted.
	handbackReport string
	// donePartial is true when the last hand-back said complete:false.
	donePartial bool
	evidence    *protocol.WorkerEvidence
	// continueCount and fixCount count the messages from the owner after a
	// hand-back with complete:no and complete:yes. continueRun and fixRun are
	// the part since this run began, which the gateway counts as one run.
	continueCount, fixCount int
	continueRun, fixRun     int
	taskID                  string
	midrunCount, midrunRun  int
	noticeSinceRequest      bool
	midrunSeen              bool
	// autoContinue turns the daemon's own continues on or off. autoContinueCount
	// and autoContinueRun count those it sent; separate from the caller's.
	autoContinue      bool
	autoContinueCount int
	autoContinueRun   int
	// pendingHandback is true after a hand-back, until the worker hands back
	// again; pendingPartial says whether that hand-back was partial.
	pendingHandback, pendingPartial bool
	// difficultyAsked is what the caller asked for, difficultyUsed the level
	// the chooser used. They are kept for the difficulty calibration.
	difficultyAsked, difficultyUsed string
	touches                         string
	// struggle is the sign count of this run, read once at run end from the
	// harness's own trace (decision 0133). struggleDone says it was read.
	struggle     protocol.Struggle
	struggleDone bool
	// taskFull is the full first task (at most 64 KiB), kept in the record so
	// that a new worker can continue it.
	taskFull string
	// unfinished is true when the daemon committed the unfinished changes of
	// the worktree on the worker's branch.
	unfinished bool
	// leftAsIs is true when the worktree had a merge, rebase, cherry-pick or
	// revert in progress, so the daemon did not commit it.
	leftAsIs          bool
	excludedLeftovers string
	// changeID is the delivery change of the worker's work: the one its last
	// complete hand-back recorded, or the one a continue carried over.
	changeID string
	// fixes is the change ID the start named as the one it fixes, empty when
	// it named none. A hand-back with one gets no defect hint.
	fixes string
	// permissions is the level the worker was started with (decision 0020).
	permissions string
	// osSandbox is the OS wrapper the worker runs in ("bubblewrap"), or ""
	// (docs/design/worker-os-sandbox.md). Kept in its folder (osSandboxMarker).
	osSandbox string
	// toolchainDirs are the extra writable folders passed to agy.
	toolchainDirs []string
	// apiBase and apiPass reach the harness's own server, for its statistics.
	// They are kept in memory only.
	apiBase, apiPass        string
	apiVersion, apiIdentity string
	// discardStdout drops the standard output of the process: Pi's rpc mode
	// writes its whole event stream there, which nobody reads.
	discardStdout bool
	// ref is the harness's own handle for the worker: the background session
	// ID of a Claude worker, the address of a Codex app server. It is kept in
	// the database, so an adopted worker can be found again.
	ref string
	// failure is the cleaned error text of a failed model call, set when the
	// worker ends with StopModelFails.
	failure string
	// info is the worker part of the session, made once so that a comparison
	// of session infos does not see a change each time.
	info *protocol.WorkerInfo
	// usage is the last figures the harness reported, and the copy in info.
	contextTokens, contextLimit, totalTokens int64
	// codex is the connection to the worker's Codex app server.
	codex        *codexConn
	agy          *agyConn
	agyProcessMu sync.Mutex // serialize process starts with the polite stop
	agyStarting  bool       // a between-turn process restart owns its process handle
	exitErr      error      // process wait error, guarded by wmu
	// piLimit is the context window of the Pi model the session file names.
	// It is read from `pi --list-models` once and kept for the worker.
	piLimitModel        string
	piLimit             int64
	claudeTranscriptOff int64
	exited              chan struct{} // closed when an own child is reaped; nil if adopted
	backgroundStart     bool          // a start whose response was sent before launch
	startDone           chan struct{} // closed after deferred task delivery
	ready               chan struct{} // closed when the session has a gateway ID
	readyOnce           sync.Once
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

// failer is a launcher that can tell that the model call of a worker failed
// (a provider, billing or auth error), so the worker can end with a notice
// instead of waiting for the idle limit. It returns the error text, or "".
type failer interface {
	failure(ctx context.Context, d *Daemon, w *worker) (string, bool)
}

// ender is a launcher that has more to clean up when a worker ends.
type ender interface {
	end(d *Daemon, w *worker)
}

// resumer starts a worker again on the harness session it had before its
// hand-back. It returns once the session is registered again (w.ready is
// closed). A harness that cannot resume a session does not implement it.
type resumer interface {
	resume(ctx context.Context, d *Daemon, w *worker) error
}

// struggler is a launcher that can count the signs that a run fought its
// instructions or its environment, from the harness's own tool record. A
// harness without a tool-call record the daemon reads does not implement it,
// and the run is reported as "no trace" (decision 0133).
type struggler interface {
	struggle(ctx context.Context, d *Daemon, w *worker) protocol.Struggle
}

func (d *Daemon) workerInfo(w *worker) *protocol.WorkerInfo {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if w.info == nil {
		w.info = &protocol.WorkerInfo{ID: w.id, StartRef: w.startRef, Owner: w.owner, Project: w.project, StartedAt: w.startedAt.Unix(), Task: w.taskText, Title: w.title,
			Permissions: protocol.WorkerPermissionsOrDefault(w.permissions)}
	}
	// The figures follow the info value, so every reader sees one version.
	// w.info may already be held by a session that another goroutine is
	// encoding (noteAsk hands it out), so never write into it: replace it.
	next := *w.info
	next.RunGeneration = w.runGeneration
	next.ContextTokens, next.ContextLimit, next.Tokens = w.contextTokens, w.contextLimit, w.totalTokens
	next.OSSandbox = w.osSandbox
	next.Continue, next.Fix = w.continueCount, w.fixCount
	next.AutoContinue = w.autoContinueCount
	next.DifficultyAsked, next.DifficultyUsed = w.difficultyAsked, w.difficultyUsed
	next.Touches = splitTouches(w.touches)
	if w.struggleDone {
		st := w.struggle
		next.Struggle = &st
	}
	w.info = &next
	// A copy: the agent list and the session info may outlive this call, and
	// noteAsk replaces the task under wmu. No one changes a value that a
	// response may still be writing (a data race the race detector sees).
	cp := *w.info
	return &cp
}

// noteWorkerUsage records the figures the harness reported and pushes the
// worker's session when one of them changed, so a TUI on another machine sees
// them too. It reports whether the run's token count grew, which means the
// worker is still working (checkWorkers uses it for the idle limit).
func (d *Daemon) noteWorkerUsage(w *worker, st workerStats) (grew bool) {
	d.wmu.Lock()
	st = lastWorkerContext(w, st)
	// A missing read must not reset activity tracking either: recovering the
	// same total on the next tick is not new work.
	st.TotalTokens = max(w.totalTokens, st.TotalTokens)
	if w.contextTokens == st.ContextTokens && w.contextLimit == st.ContextLimit && w.totalTokens == st.TotalTokens {
		d.wmu.Unlock()
		return false
	}
	grew = st.TotalTokens > w.totalTokens
	w.contextTokens, w.contextLimit, w.totalTokens = st.ContextTokens, st.ContextLimit, st.TotalTokens
	if w.info == nil {
		w.info = &protocol.WorkerInfo{ID: w.id, StartRef: w.startRef, Owner: w.owner, Project: w.project, StartedAt: w.startedAt.Unix(), Task: w.taskText, Title: w.title}
	}
	cp := *w.info
	cp.ContextTokens, cp.ContextLimit, cp.Tokens = w.contextTokens, w.contextLimit, w.totalTokens
	w.info = &cp
	sessionID := w.sessionID
	d.wmu.Unlock()
	if ls := d.setSessionWorker(sessionID, &cp); ls != nil {
		go d.pushSessionChanged(ls)
	}
	return grew
}

// keepWorkerActive restarts the idle clock of a worker that is working, and
// saves the row as touchWorker does, so an adopted worker does not look idle.
func (d *Daemon) keepWorkerActive(w *worker, now time.Time) {
	d.wmu.Lock()
	w.lastActive = now
	if now.Sub(w.savedAt) > 15*time.Second {
		d.saveWorkerLocked(w)
	}
	d.wmu.Unlock()
}

// sessionBusy reports whether the harness says the worker's session is busy.
// A busy session is working, whatever its last message or token figure says.
func (d *Daemon) sessionBusy(harness, native string) bool {
	if native == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ls := d.sessions[key(harness, native)]
	return ls != nil && ls.info.Status == "busy"
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
	d.writeWorkerCommitSession(w)
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

// beginWorkerMessage marks the workers named by a message as having a send in
// flight. The caller clears the marks with endWorkerMessage once the gateway
// has answered, and checkWorkers treats a marked worker as working: the idle
// limit must not end a worker before the message that restarts its idle clock
// lands.
func (d *Daemon) beginWorkerMessage(sessionIDs ...string) []*worker {
	var marked []*worker
	d.wmu.Lock()
	for _, id := range sessionIDs {
		if id == "" {
			continue
		}
		for _, w := range d.workers {
			if w.sessionID == id {
				w.sendsInFlight++
				marked = append(marked, w)
				break
			}
		}
	}
	d.wmu.Unlock()
	return marked
}

// endWorkerMessage clears the marks made by beginWorkerMessage.
func (d *Daemon) endWorkerMessage(marked []*worker) {
	if len(marked) == 0 {
		return
	}
	d.wmu.Lock()
	for _, w := range marked {
		if w.sendsInFlight > 0 {
			w.sendsInFlight--
		}
	}
	d.wmu.Unlock()
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
	if w.pid == 0 || w.stopping {
		return // still starting, or its final record is being saved
	}
	if err := d.st.saveWorker(d.rowLocked(w, "")); err != nil {
		d.Logf("worker %s: save: %v", w.id, err)
	}
}

// rowLocked is the database row of a worker. Callers hold d.wmu.
func (d *Daemon) rowLocked(w *worker, ended string) workerRow {
	return workerRow{RunGeneration: w.runGeneration, Evidence: w.evidence, ID: w.id, Harness: w.harness, Project: w.project, Path: w.path, Model: w.model,
		Owner: w.owner, NativeID: w.nativeID, SessionID: w.sessionID, PID: w.pid, PGID: w.pgid, StartToken: w.token,
		StartedAt: w.startedAt, LastActive: w.lastActive, EndedReason: ended, MainPath: w.mainPath, Worktree: w.worktree,
		Branch: w.branch, Base: w.base, TaskMsg: w.taskMsg, TaskText: w.taskText, Title: w.title, DoneStatus: w.doneStatus, DonePartial: w.donePartial,
		Ref: w.ref, TaskFull: w.taskFull, Permissions: w.permissions,
		Continue: w.continueCount, Fix: w.fixCount, ContinueRun: w.continueRun, FixRun: w.fixRun,
		AutoContinue: w.autoContinue, AutoContinueCount: w.autoContinueCount, AutoContinueRun: w.autoContinueRun,
		PendingHandback: w.pendingHandback, PendingPartial: w.pendingPartial,
		DifficultyAsked: w.difficultyAsked, DifficultyUsed: w.difficultyUsed, Touches: w.touches,
		TaskID: w.taskID, MidrunCount: w.midrunCount, MidrunRun: w.midrunRun, NoticeSinceRequest: w.noticeSinceRequest, MidrunSeen: w.midrunSeen, Failure: w.failure, ChangeID: w.changeID,
		Struggle: w.runStruggle()}
}

// runStruggle returns the run's sign count, or nil before it was read.
// Callers hold d.wmu.
func (w *worker) runStruggle() *protocol.Struggle {
	if !w.struggleDone {
		return nil
	}
	st := w.struggle
	return &st
}

// ---- starting ----

// projectWorkerRules reads the main working tree copy, so local project rules
// apply even before they are committed. Limit the read and remove control bytes.
func projectWorkerRules(path string) string {
	name := filepath.Join(path, ".agent-net", "worker.md")
	fi, err := os.Stat(name)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8192))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, strings.ToValidUTF8(string(b), "")))
}

// startWorker handles "worker.launch" from the gateway.
func (d *Daemon) startWorker(ctx context.Context, l protocol.WorkerLaunch) (out protocol.WorkerLaunched, err error) {
	snap := d.SettingsSnapshot()
	set := d.workerSettingsWith(snap, l.Project)
	refuse := func(format string, a ...any) (protocol.WorkerLaunched, error) {
		err := fmt.Errorf(format, a...)
		d.Logf("worker.refused project=%s harness=%s owner=%s reason=%q", l.Project, l.Harness, l.Owner, err.Error())
		return protocol.WorkerLaunched{}, err
	}
	if d.cfg.NoLaunch {
		return refuse("this machine does not accept workers (it was set up with --no-launch)")
	}
	if l.RequireReadOnly && (l.Permissions != protocol.WorkersReadOnly || !protocol.WorkerReadOnlySupported(l.Harness)) {
		return refuse("worker cannot enforce read-only access")
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
	if e := validateWorkerIsolation(snap, l.Project, l.Isolation); e != nil {
		return refuse("%v", e)
	}
	if l.Base != "" && l.Isolation != protocol.IsolationWorktree {
		return refuse("a base branch needs worktree isolation")
	}
	if l.Harness == "pi" && l.Isolation != protocol.IsolationWorktree {
		return refuse("a Pi worker needs isolation %q: Pi has no permission prompts, so it must not work in your own checkout", protocol.IsolationWorktree)
	}
	if l.Owner == "" {
		return refuse("a worker needs an owner")
	}
	// The OS sandbox is checked before the queue too, so a start that cannot
	// run in it is refused now, not after the wait.
	if _, err := d.osSandboxChoice(l.Project, l.Harness, l.Permissions, false); err != nil {
		return refuse("%v", err)
	}
	// No room now: the start waits in the queue (decision 0018).
	if q := d.admitOrQueue(l); q != nil {
		if !q.Queued {
			return refuse("%s", q.Reason)
		}
		return *q, nil
	}
	out, err = d.runWorkerStartChecked(ctx, l, set, true, snap)
	if err != nil {
		d.clearStartReservation()
	}
	return out, err
}

func validateWorkerIsolation(snap settings.Snapshot, repo, isolation string) error {
	if snap.String("workers.isolation_minimum", settings.Where{Repo: repo}) == "worktree-required" && isolation != protocol.IsolationWorktree {
		return fmt.Errorf("workers.isolation_minimum requires worktree isolation for %s", repo)
	}
	return nil
}

// runWorkerStart starts a worker that admission has let through. The checks
// of startWorker are done; l has its isolation filled in.
func (d *Daemon) runWorkerStart(ctx context.Context, l protocol.WorkerLaunch) (protocol.WorkerLaunched, error) {
	snap := d.SettingsSnapshot()
	set := d.workerSettingsWith(snap, l.Project)
	if l.Isolation == "" && isGitRepo(ctx, l.Path) {
		l.Isolation = protocol.IsolationWorktree
	}
	if err := validateWorkerIsolation(snap, l.Project, l.Isolation); err != nil {
		d.releaseWorkerBase(l.Base)
		return protocol.WorkerLaunched{}, err
	}
	if l.Isolation == protocol.IsolationWorktree {
		set.StartTimeout = set.BackgroundStartTimeout
	}
	return d.runWorkerStartChecked(ctx, l, set, false, snap)
}

func (d *Daemon) runWorkerStartChecked(ctx context.Context, l protocol.WorkerLaunch, set WorkerSettings, async bool, snap settings.Snapshot) (out protocol.WorkerLaunched, err error) {
	selection := protocol.ParseModelSelection(l.Harness, l.Provider, l.Model, l.Effort)
	l.Model = selection.LegacyModel()
	transferredBase := false
	defer func() {
		if !transferredBase {
			d.releaseWorkerBase(l.Base)
		}
	}()
	refuse := func(format string, a ...any) (protocol.WorkerLaunched, error) {
		err := fmt.Errorf(format, a...)
		d.Logf("worker.refused project=%s harness=%s owner=%s reason=%q", l.Project, l.Harness, l.Owner, err.Error())
		return protocol.WorkerLaunched{}, err
	}
	ln := d.launchers[l.Harness]
	if ln == nil {
		return refuse("this daemon cannot start %q workers", l.Harness)
	}
	sandbox, err := d.osSandboxChoice(l.Project, l.Harness, l.Permissions, false)
	if err != nil {
		return refuse("%v", err)
	}
	rules := projectWorkerRules(l.Path)
	w := &worker{runGeneration: 1, id: protocol.NewID("w"), startRef: l.StartRef, harness: l.Harness, osSandbox: sandbox, project: l.Project, path: l.Path, model: l.Model, owner: l.Owner, ownerName: l.OwnerName,
		startedAt: time.Now(), ready: make(chan struct{}), taskText: protocol.WorkerTaskSummary(first(l.RecordTask, l.Task)),
		title:       l.Title,
		taskFull:    boundTask(first(l.RecordTask, l.Task)),
		permissions: protocol.WorkerPermissionsOrDefault(l.Permissions), difficultyAsked: l.DifficultyAsked, difficultyUsed: l.DifficultyUsed, touches: strings.Join(l.Touches, ","),
		autoContinue: protocol.AutoContinueEnabled(l.AutoContinue), changeID: l.ChangeID, fixes: l.Fixes, mcpServers: l.MCPServers}
	if l.TaskID != "" {
		w.taskID = l.TaskID
		if l.ContinuationPartial {
			w.continueCount, w.continueRun = 1, 1
		} else {
			w.fixCount, w.fixRun = 1, 1
		}
	}
	worktree, branch := "", ""
	if l.Isolation == protocol.IsolationWorktree {
		worktree, branch = filepath.Join(d.cfg.StateDir, "worktrees", w.id), protocol.WorktreeBranchWithSettings(w.id, snap)
	}
	if len(protocol.WorkerTaskBodyWithSettingsForRepo(l.Harness, l.Task, worktree, branch, rules, "", "", snap, l.Project)) > protocol.MaxBodyBytes {
		return refuse("the task and project rules exceed the message limit")
	}
	w.lastActive = w.startedAt
	d.wmu.Lock()
	if len(d.workers) >= set.Max {
		n := len(d.workers)
		d.wmu.Unlock()
		return refuse("this machine already runs %d workers, which is the limit", n)
	}
	d.workers[w.id] = w
	d.wmu.Unlock()

	if async && l.Isolation == protocol.IsolationWorktree {
		d.wmu.Lock()
		w.sessionID = protocol.NewID("s")
		w.backgroundStart = true
		w.startDone = make(chan struct{})
		sid := w.sessionID
		d.wmu.Unlock()
		d.wg.Add(1)
		transferredBase = true
		go func() {
			defer d.wg.Done()
			defer d.releaseWorkerBase(l.Base)
			defer close(w.startDone)
			backgroundSet := set
			backgroundSet.StartTimeout = set.BackgroundStartTimeout
			out, err := d.finishWorkerStart(d.ctoCtx, l, backgroundSet, w, rules)
			if err == nil && out.SessionID != sid {
				err = fmt.Errorf("the gateway did not reserve worker session %s", sid)
				d.endWorker(w, protocol.StopStartFails)
			}
			e := queuedStart{ID: l.StartRef, Owner: l.Owner, Project: l.Project, Harness: l.Harness, Model: l.Model, Task: l.Task, Permissions: l.Permissions, FromCTO: l.FromCTO, QueuedAt: w.startedAt}
			if err != nil {
				d.clearStartReservation()
				d.notStarted(d.ctoCtx, e, err.Error())
				return
			}
			err = d.sendQueuedStart(d.ctoCtx, w, protocol.WorkerQueuedStart{Ref: l.StartRef, Owner: l.Owner, SessionID: out.SessionID, Harness: l.Harness, Project: l.Project, Task: l.Task, Worktree: out.Worktree, Branch: out.Branch, ProjectRules: out.ProjectRules, Permissions: l.Permissions, OSSandbox: out.OSSandbox, FromCTO: l.FromCTO})
			if errors.Is(err, errWorkerEnded) {
				d.notStarted(d.ctoCtx, e, "the worker ended before its task was sent")
				return
			}
			if err != nil {
				d.endWorker(w, protocol.StopStartFails)
				d.notStarted(d.ctoCtx, e, "the worker started, but its task could not be sent: "+err.Error())
				return
			}
			body := protocol.WorkerStartedText(out.SessionID, l.Harness, l.Model, l.Project, d.hostName(), time.Since(w.startedAt)) + protocol.WorkerPermissionsLine(l.Permissions, l.Harness, out.OSSandbox) + "\n"
			if _, err := d.send(d.ctoCtx, protocol.MsgSend{Sender: out.SessionID, Recipient: l.Owner, Type: protocol.TypeNotification, Body: body}); err != nil {
				d.Logf("worker %s: started notice: %v", w.id, err)
			}
		}()
		return protocol.WorkerLaunched{Starting: true, SessionID: sid, WorkerID: w.id, Worktree: worktree, Branch: branch, ProjectRules: rules, OSSandbox: w.osSandbox}, nil
	}
	return d.finishWorkerStart(ctx, l, set, w, rules)
}

func (d *Daemon) finishWorkerStart(ctx context.Context, l protocol.WorkerLaunch, set WorkerSettings, w *worker, rules string) (protocol.WorkerLaunched, error) {
	if err := d.recordDeliveryRisk(w.id, l.Assessment); err != nil {
		d.endWorker(w, protocol.StopStartFails)
		return protocol.WorkerLaunched{}, fmt.Errorf("record delivery risk: %w", err)
	}
	ln := d.launchers[l.Harness]
	cctx, cancel := context.WithTimeout(ctx, set.StartTimeout)
	defer cancel()
	var err2 error
	if l.Isolation == protocol.IsolationWorktree {
		err2 = d.createWorktree(cctx, w, l.Base)
	}
	if err2 == nil {
		err2 = ln.start(cctx, d, w)
	}
	if err := err2; err != nil {
		tail := d.workerLogTail(w)
		d.endWorker(w, protocol.StopStartFails)
		if tail != "" {
			err = fmt.Errorf("%w (harness output: %s)", err, tail)
		}
		err = d.explainStartFailure(ctx, l.Harness, l.Model, err)
		d.Logf("worker.refused project=%s harness=%s owner=%s reason=%q", l.Project, l.Harness, l.Owner, err.Error())
		return protocol.WorkerLaunched{}, err
	}
	d.wmu.Lock()
	sid, wt, br, toolchains := w.sessionID, w.worktree, w.branch, strings.Join(w.toolchainDirs, ", ")
	d.wmu.Unlock()
	d.recordWorkerAssessment(w, l.Assessment)
	d.Logf("worker.started worker=%s session=%s project=%s path=%s harness=%s model=%s owner=%s pid=%d worktree=%s branch=%s toolchains=%q",
		w.id, sid, w.project, w.path, w.harness, w.model, w.owner, w.pid, wt, br, toolchains)
	return protocol.WorkerLaunched{SessionID: sid, WorkerID: w.id, Worktree: wt, Branch: br, ProjectRules: rules, OSSandbox: w.osSandbox}, nil
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
	return d.spawnWorkerIO(w, bin, args, env, stdin, nil)
}

// spawnWorkerIO also streams stdout to the harness event reader. Wait drains
// the reader before closing exited, so the last result and usage are retained.
func (d *Daemon) spawnWorkerIO(w *worker, bin string, args []string, env []string, stdin *os.File, stdout io.Writer) error {
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
	// The OS sandbox: a Pi or OpenCode worker of a project that has it on
	// never starts without it.
	d.wmu.Lock()
	sandbox := w.osSandbox
	d.wmu.Unlock()
	if sandbox == "" && (w.harness == "pi" || w.harness == "opencode") && d.cfg.Workers.osSandboxOn(w.project) {
		logf.Close()
		return errors.New("the OS sandbox is on for this project (workers.os_sandbox in node.json), but this start did not apply it")
	}
	if sandbox != "" {
		var werr error
		if bin, args, env, werr = d.osSandboxWrap(w, bin, args, env); werr != nil {
			logf.Close()
			return werr
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = w.path
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if stdout != nil {
		if f, ok := stdout.(*os.File); ok {
			cmd.Stdout = f // inherited file, with no daemon-owned copier pipe
		} else {
			cmd.Stdout = io.MultiWriter(logf, stdout)
		}
	}
	if w.discardStdout {
		cmd.Stdout = nil // /dev/null
	}
	// A session of its own (which is also a group of its own, with the process
	// ID as the group ID). The worker outlives the daemon, so nothing that
	// signals the daemon's group or session may reach it: launchd, a terminal
	// hangup, a service manager's group kill.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("cannot run %s: %w", bin, err)
	}
	exited := make(chan struct{})
	d.wmu.Lock()
	w.pid, w.pgid, w.exited = cmd.Process.Pid, cmd.Process.Pid, exited
	w.exitErr = nil
	d.wmu.Unlock()
	go func() {
		err := cmd.Wait()
		logf.Close()
		d.wmu.Lock()
		w.exitErr = err
		d.wmu.Unlock()
		close(exited)
	}()
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
	exited, pid, token, harness := w.exited, w.pid, w.token, w.harness
	d.wmu.Unlock()
	if pid == 0 {
		return false
	}
	if exited != nil {
		select {
		case <-exited:
			return true
		default:
			if harness == "agy" {
				// Wait drains the stream reader. The process can disappear
				// before its final result and usage have reached that reader.
				return false
			}
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
	deadline := time.Now().Add(d.workerSettings("").KillGrace)
	for time.Now().Before(deadline) && !d.gone(w) {
		time.Sleep(25 * time.Millisecond)
	}
	syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 40 && !d.gone(w); i++ {
		time.Sleep(25 * time.Millisecond)
	}
}

// endWorker stops a worker and forgets it. It is safe to call twice.
// A worker that ends after its hand-back keeps its row and folder, so a
// message from its owner starts the harness session again (W10).
// workerEnded returns a channel that is closed when the worker starts to end.
func (d *Daemon) workerEnded(w *worker) <-chan struct{} {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if w.endedCh == nil {
		w.endedCh = make(chan struct{})
		if w.stopping {
			close(w.endedCh)
		}
	}
	return w.endedCh
}

// closeEndedLocked closes the ended channel. The caller holds wmu and has just
// set stopping, which only one caller does.
func (d *Daemon) closeEndedLocked(w *worker) {
	if w.endedCh == nil {
		w.endedCh = make(chan struct{})
	}
	close(w.endedCh)
}

func (d *Daemon) endWorker(w *worker, reason string) {
	d.endWorkerWhen(w, reason, nil)
}

// endWorkerWhen is endWorker with a last check: when is called under the
// worker lock just before the end starts, and returning false cancels it. The
// idle check uses it to look again for activity that began after its snapshot,
// so a message that starts at the idle deadline still keeps the worker alive.
func (d *Daemon) endWorkerWhen(w *worker, reason string, when func() bool) {
	if done := d.beginWorkerEnd(w, reason, when); done != nil {
		<-done
	}
}

// beginWorkerEnd stops the worker synchronously and queues its notice and
// cleanup. The worker loop leaves those to the queue; direct callers wait.
func (d *Daemon) beginWorkerEnd(w *worker, reason string, when func() bool) <-chan struct{} {
	d.wmu.Lock()
	if w.stopping || (when != nil && !when()) {
		d.wmu.Unlock()
		return nil
	}
	w.stopping = true
	d.closeEndedLocked(w)
	// A failed hand-back is the run's failure: worker.stopped carries its text,
	// so the gateway can tell a provider limit from a code failure (decision
	// 0145). A hand-back that did not fail clears an older text.
	if reason == protocol.StopHandback {
		w.failure = handbackFailure(w.doneStatus, w.handbackReport)
	}
	if err := d.st.deleteWorkerIdleNotice(w.id); err != nil {
		d.Logf("worker %s: remove idle notice: %v", w.id, err)
	}
	sid, harness, project, id, dir, started := w.sessionID, w.harness, w.project, w.id, w.dir, w.startedAt
	done := w.doneStatus != ""
	d.wmu.Unlock()
	// A hand-back ends the worker with its session kept, and so does any end
	// that races the hand-back's own end.
	resumable := reason == protocol.StopHandback || done
	// A worker that ends with no hand-back still tells its owner (W11). The
	// harness statistics need the process, so they are read before the kill.
	status, note := d.noticeFor(reason)
	if reason == protocol.StopModelFails || reason == protocol.StopRunFailed {
		d.wmu.Lock()
		note += ": " + w.failure
		d.wmu.Unlock()
	}
	// Lost workers are reported from their persisted row by reportWorkerEnds.
	sendNotice := !done && status != "" && sid != "" && reason != protocol.StopLost
	var stats workerStats
	if sendNotice {
		stats = d.workerStats(w)
		// Read the run's trace before the harness process goes, so the notice
		// and the run record carry the same counts (decision 0133).
		d.noteWorkerStruggle(context.Background(), w)
	}
	// What the worker cost is read before the process goes (decision 0018).
	if life, ok := d.treeLife(w.pid); ok {
		d.recordWorkerCost(w.harness, w.model, life, time.Since(started))
	}
	if st, ok := d.launchers[w.harness].(stopper); ok && w.pid != 0 {
		st.stop(d, w)
	}
	d.killGroup(w)
	if time.Since(started) < workerGapHoldMinAge {
		d.clearGapAfterShortWorker(w)
	}
	if en, ok := d.launchers[w.harness].(ender); ok {
		en.end(d, w)
	}
	d.wmu.Lock()
	hasEvidence := w.evidence != nil
	d.wmu.Unlock()
	if !done || !hasEvidence {
		d.captureWorkerEvidence(w, nil)
	}
	d.wmu.Lock()
	row := d.rowLocked(w, reason)
	row.Resumable = resumable
	if w.pid != 0 || (reason == protocol.StopStartFails && w.backgroundStart) {
		// Kept until the gateway has been told (and, for a hand-back, until it
		// is too old to resume).
		if err := d.st.saveWorker(row); err != nil {
			d.Logf("worker %s: save: %v", id, err)
		}
	} else {
		d.st.deleteWorker(id)
	}
	d.wmu.Unlock()
	endStatus, endNote := d.endFacts(w, reason)
	d.rememberEnded(w, endStatus, endNote, time.Now())
	ran := time.Since(started)
	return d.queueWorkerEnd(func() {
		result := workerEndCommitResult{}
		if w.worktree != "" {
			unlock := d.lockWorktree(w.worktree)
			if harness == "agy" {
				result = d.saveAgyWorkerEnd(w, reason)
			} else {
				result = d.commitWorkerEndWIP(context.Background(), w.worktree, w.branch, reason)
			}
			unlock()
		}
		if result.Failed != "" {
			d.Logf("worker %s: end WIP commit: %s", id, result.Failed)
		}
		if result.Skipped == "git operation in progress" {
			d.wmu.Lock()
			w.leftAsIs = true
			d.wmu.Unlock()
		}
		wipDetail := result.noticeDetail()
		if result.Legacy && result.SHA != "" {
			wipDetail = "" // Saved carries Antigravity's historical notice text.
		}
		if sendNotice {
			n := d.notice(w, status, note, stats)
			n.WIPDetail = wipDetail
			// Time waiting behind another notice is not worker run time.
			n.RunTime = ran
			if _, err := d.sendNotice(context.Background(), w, n, ""); err != nil {
				d.Logf("worker %s: notice: %v", id, err)
			}
		}
		d.finishWorktree(w)
		// Keep the worker registered until its worktree and branch are settled.
		// Continue and resume must not race cleanup of the old branch.
		d.wmu.Lock()
		delete(d.workers, w.id)
		d.wmu.Unlock()
		keepDirtyWorktree := result.keepWorktree()
		if dir != "" && !resumable && !keepDirtyWorktree {
			os.RemoveAll(dir)
		}
		if resumable {
			d.Logf("worker.stopped worker=%s session=%s project=%s harness=%s reason=%s ran=%s ended_after_handback=1",
				id, sid, project, harness, reason, time.Since(started).Round(time.Second))
		} else {
			d.Logf("worker.stopped worker=%s session=%s project=%s harness=%s reason=%s ran=%s", id, sid, project, harness, reason,
				time.Since(started).Round(time.Second))
		}
		d.reportWorkerEnds(context.Background())
	})
}

// reportWorkerEnds tells the gateway about workers that ended, once each. A
// worker that became a session keeps its row (marked as reported) until it is
// past the longest run time: the row says that the harness session is a
// worker's, so the plugin of another server may not register it, and a
// hand-back row is what a follow-up starts again (W19).
func (d *Daemon) reportWorkerEnds(ctx context.Context) {
	rows, err := d.st.workers()
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.EndedReason == "" || r.Reported {
			continue
		}
		if r.SessionID == "" {
			d.st.deleteWorker(r.ID) // it never became a session: the gateway has nothing to update
			continue
		}
		// The gateway link and the worker loop both reach here after a restart.
		// Claim the row, so only one of them tells the owner and the gateway.
		if !d.claimWorkerReport(r.ID) {
			continue
		}
		if r.EndedReason == protocol.StopLost {
			d.lostNotice(ctx, r)
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var complete *bool
		if r.DoneStatus != "" {
			c := !r.DonePartial
			complete = &c
		}
		var proofReason string
		_ = d.st.db.QueryRow(`SELECT reason FROM worker_visual_reports WHERE worker_id=? AND generation=?`, r.ID, r.RunGeneration).Scan(&proofReason)
		_, err := d.call(cctx, "worker.stopped", protocol.WorkerStopped{ProofUnavailable: proofReason, RunGeneration: r.RunGeneration, Evidence: r.Evidence, SessionID: r.SessionID, WorkerID: r.ID,
			Project: r.Project, Harness: r.Harness, Reason: r.EndedReason, Model: r.Model, Failure: r.Failure,
			Status: r.DoneStatus, Complete: complete, StartedAt: r.StartedAt.Unix(),
			Continue: r.Continue, Fix: r.Fix, ContinueRun: r.ContinueRun, FixRun: r.FixRun,
			AutoContinue: r.AutoContinueCount, AutoContinueRun: r.AutoContinueRun,
			DifficultyAsked: r.DifficultyAsked, DifficultyUsed: r.DifficultyUsed, Touches: splitTouches(r.Touches), TaskID: r.TaskID, MidrunRun: r.MidrunRun, Struggle: r.Struggle})
		cancel()
		var re RemoteError
		switch {
		case err == nil, errors.As(err, &re):
			// Told, or refused for good (an older gateway, an unknown session).
			// The claim stays, so a caller that read the row before this report
			// cannot repeat it.
			d.st.markWorkerReported(r.ID, r.EndedReason, r.RunGeneration)
		default:
			d.releaseWorkerReport(r.ID)
			return // not connected: try again at the next connect
		}
	}
}

// claimWorkerReport takes the right to report one ended worker. It returns
// false when another caller is reporting or has reported that worker. The
// claim is kept after a report that was sent, because a caller may have read
// the row before it was marked. A report that failed releases the claim, so
// the next connect tries again.
func (d *Daemon) claimWorkerReport(id string) bool {
	d.reportMu.Lock()
	defer d.reportMu.Unlock()
	if d.reporting[id] {
		return false
	}
	if d.reporting == nil {
		d.reporting = make(map[string]bool)
	}
	d.reporting[id] = true
	return true
}

func (d *Daemon) releaseWorkerReport(id string) {
	d.reportMu.Lock()
	delete(d.reporting, id)
	d.reportMu.Unlock()
}

// stopWorkerSession handles "worker.kill" from the gateway. A queued start
// (a q_ ID) is cancelled instead.
func (d *Daemon) stopWorkerSession(k protocol.WorkerKill, validate ...bool) error {
	sessionID := k.Session
	if protocol.IsQueueID(sessionID) {
		return d.cancelQueued(sessionID, k.Requester, k.AsOwner, validate...)
	}
	w := d.workerBySession(sessionID)
	if w == nil {
		if err := d.cancelQueued(sessionID, k.Requester, k.AsOwner, validate...); err == nil {
			return nil
		}
		return fmt.Errorf("no worker with session %s runs on this machine", sessionID)
	}
	if len(validate) > 0 && validate[0] {
		return nil
	}
	d.endWorker(w, protocol.StopRequested)
	return nil
}

// resumeWorkerSession handles "worker.resume" from the gateway: the owner of
// an ended worker sent it a message, so the worker's own harness session is
// started again (W10). Only the owner may ask; the gateway checks it and so
// does this function.
func (d *Daemon) resumeWorkerSession(ctx context.Context, sessionID, requester string) (*protocol.WorkerResumeResult, error) {
	d.resumeMu.Lock()
	defer d.resumeMu.Unlock()
	// A hand-back ends the worker right after its notice, and the owner's next
	// message may arrive while the old process is still being killed. Wait for
	// the end to finish, then start the session again.
	for {
		w := d.workerBySession(sessionID)
		if w == nil {
			break
		}
		d.wmu.Lock()
		owner, stopping, done := w.owner, w.stopping, w.doneStatus != ""
		d.wmu.Unlock()
		if owner != requester {
			return nil, fmt.Errorf("only the session that started worker %s can start it again", sessionID)
		}
		// A running worker with no hand-back just gets the message. A worker
		// that handed back is ending now, even if its end has not started yet:
		// wait for it, then start the session again.
		if !stopping && !done {
			return &protocol.WorkerResumeResult{}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	row, ok, err := d.st.workerBySession(sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no worker with session %s is waiting to be started again on this machine", sessionID)
	}
	if row.EndedReason != protocol.StopHandback || !row.Resumable {
		return nil, fmt.Errorf("worker %s ended with reason %s, so it cannot be started again: only a worker that ended after its hand-back can", sessionID, row.EndedReason)
	}
	if requester != row.Owner {
		return nil, fmt.Errorf("only the session that started worker %s can start it again", sessionID)
	}
	if err := checkWorkerWorktree(&row); err != nil {
		return nil, err
	}
	set := d.workerSettings("")
	if row.Worktree == "" && row.StartedAt.Add(set.MaxAge).Before(time.Now()) {
		// Retention cleanup owns deletion and retries failed harness calls.
		return nil, fmt.Errorf("worker %s started more than %s ago (the longest run time), so it cannot be started again", sessionID, set.MaxAge)
	}
	ln := d.launchers[row.Harness]
	if ln == nil {
		return nil, fmt.Errorf("this daemon cannot start %q workers", row.Harness)
	}
	if _, ok := ln.(resumer); !ok {
		return nil, fmt.Errorf("a %s worker cannot be started again: %s", row.Harness, cannotResumeReason(row.Harness))
	}

	qres, err := d.admitOrQueueResume(&row)
	if err != nil {
		return nil, err
	}
	if qres != nil {
		return qres, nil
	}
	defer d.releaseWorkerBase(row.Base)

	if _, err := d.runWorkerResume(ctx, row, set); err != nil {
		return nil, err
	}
	return &protocol.WorkerResumeResult{}, nil
}

// runWorkerResume starts the worker process for an ended worker.
// The caller holds d.resumeMu and has already reserved the base.
func (d *Daemon) runWorkerResume(ctx context.Context, row workerRow, set WorkerSettings) (*worker, error) {
	ln := d.launchers[row.Harness]
	if ln == nil {
		return nil, fmt.Errorf("this daemon cannot start %q workers", row.Harness)
	}
	rs, ok := ln.(resumer)
	if !ok {
		return nil, fmt.Errorf("a %s worker cannot be started again: %s", row.Harness, cannotResumeReason(row.Harness))
	}
	// A follow-up stays in the OS sandbox the worker started in, and goes in
	// it when the switch is on now.
	sandbox, err := d.osSandboxChoice(row.Project, row.Harness, row.Permissions, readOSSandboxMarker(filepath.Join(d.cfg.StateDir, "workers", row.ID)) != "")
	if err != nil {
		return nil, err
	}
	w := &worker{runGeneration: row.RunGeneration + 1, id: row.ID, harness: row.Harness, project: row.Project, path: row.Path, model: row.Model, owner: row.Owner, ownerName: row.OwnerName,
		nativeID: row.NativeID, sessionID: row.SessionID, startedAt: time.Now(), lastActive: time.Now(),
		dir: filepath.Join(d.cfg.StateDir, "workers", row.ID), mainPath: row.MainPath, worktree: row.Worktree,
		branch: row.Branch, base: row.Base, taskMsg: row.TaskMsg, taskText: row.TaskText, title: row.Title, taskFull: row.TaskFull, ref: row.Ref, permissions: protocol.WorkerPermissionsOrDefault(row.Permissions),
		continueCount: row.Continue, fixCount: row.Fix, pendingHandback: row.PendingHandback, pendingPartial: row.PendingPartial,
		taskID: row.TaskID, midrunCount: row.MidrunCount, noticeSinceRequest: row.NoticeSinceRequest, midrunSeen: row.MidrunSeen,
		autoContinue: row.AutoContinue, autoContinueCount: row.AutoContinueCount,
		difficultyAsked: row.DifficultyAsked, difficultyUsed: row.DifficultyUsed, touches: row.Touches, changeID: row.ChangeID, fixes: row.Fixes, ready: make(chan struct{}), osSandbox: sandbox}
	w.logPath = filepath.Join(w.dir, "worker.log")
	d.wmu.Lock()
	d.workers[w.id] = w
	row.EndedReason, row.Reported, row.Resumable = "", false, false
	d.saveWorkerLocked(w)
	d.wmu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, set.StartTimeout)
	defer cancel()
	okStart := false
	defer func() {
		if okStart {
			return
		}
		d.wmu.Lock()
		delete(d.workers, w.id)
		d.wmu.Unlock()
		r := row
		r.RunGeneration = w.runGeneration
		r.EndedReason, r.Reported, r.Resumable = protocol.StopHandback, true, true
		if r.PID != 0 {
			d.st.saveWorker(r)
		}
	}()
	// An ended worker may have lost its clean checkout. The branch check above
	// ensures createWorktree reuses its local branch and preserves its commits.
	if w.worktree != "" {
		if _, err := os.Stat(w.worktree); os.IsNotExist(err) {
			w.path = w.mainPath
			w.worktree = ""
			if err := d.createWorktree(cctx, w); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return nil, err
	}
	// Both resume paths announce the new generation before the harness can
	// register. A delayed report from the previous process cannot end it.
	runStateSupported := true
	if _, err := d.call(cctx, "worker.run_state", protocol.WorkerRunState{Session: row.SessionID, RunGeneration: w.runGeneration}); err != nil {
		var refusal RemoteError
		if !errors.As(err, &refusal) || string(refusal) != `unknown frame type "worker.run_state"` {
			return nil, err
		}
		// An older gateway activates the worker before asking us to resume it.
		// Only its explicit unknown-method refusal permits this legacy path.
		runStateSupported = false
	}
	defer func() {
		if !okStart && runStateSupported {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			d.call(rollbackCtx, "worker.run_state", protocol.WorkerRunState{Session: row.SessionID, RunGeneration: w.runGeneration, Ended: true})
		}
	}()
	if err := rs.resume(cctx, d, w); err != nil {
		d.Logf("worker.resume_failed worker=%s session=%s harness=%s reason=%q", w.id, row.SessionID, w.harness, err.Error())
		return nil, err
	}
	okStart = true
	d.startWorkerAssessment(w, w.path, w.taskFull)
	// A resumed worker starts a new run: its next end must be reported again.
	d.releaseWorkerReport(w.id)
	d.Logf("worker.resumed worker=%s session=%s project=%s harness=%s model=%s owner=%s pid=%d",
		w.id, row.SessionID, w.project, w.harness, w.model, w.owner, w.pid)
	return w, nil
}

// cannotResumeReason says why a harness cannot open an ended session again.
func cannotResumeReason(harness string) string {
	switch harness {
	case "pi":
		return "Pi's rpc mode cannot open an existing session; the daemon starts a new one with --session-id, and Pi has no resume flag. Read the old session with `agent-net show`, or open its session file in Pi."
	}
	return "the daemon does not know how to open an existing session of this harness"
}

// dropWorkerRow forgets an ended worker: its row and its folder. The gateway
// drops its copy of the worker at the next worker.cleanup.
func (d *Daemon) dropWorkerRow(r workerRow) bool {
	d.st.removeQueuedForSession(r.SessionID)
	set := d.workerSettings("")
	if set.DeleteSessions != nil && !*set.DeleteSessions && r.Harness == "opencode" {
		// Preserve the only copy of the conversation when deletion is disabled.
		d.st.deleteWorker(r.ID)
		d.releaseWorkerReport(r.ID)
		return true
	}
	size := workerFolderBytes(filepath.Join(d.cfg.StateDir, "workers", r.ID))
	if err := os.RemoveAll(filepath.Join(d.cfg.StateDir, "workers", r.ID)); err != nil {
		d.Logf("worker.cleanup retry worker=%s: %v", r.ID, err)
		return false
	}
	d.st.deleteWorker(r.ID)
	d.Logf("worker.session.cleaned worker=%s harness=%s session=%s bytes_freed=%d", r.ID, r.Harness, r.NativeID, size)
	d.releaseWorkerReport(r.ID) // its row is gone; nothing is left to report
	return true
}

// cleanupWorkers updates gateway retention (W7). It never deletes harness
// sessions: the independent workerSessionCleanupLoop owns local retention.
func (d *Daemon) cleanupWorkers(ctx context.Context) {
	set := d.workerSettings("")
	// Retry reports that failed while the gateway was away.
	d.reportWorkerEnds(ctx)
	d.dropWorktreeSessions()
	if !d.Connected() {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Wait for the workers of the last run to be taken back: the gateway keeps
	// the records of the workers listed here.
	select {
	case <-d.adopted:
	case <-ctx.Done():
		return
	}
	retained, err := d.retainedWorkerSessionIDs()
	if err != nil {
		d.Logf("worker.cleanup: cannot read retained workers: %v", err)
		return
	}
	if _, err := d.call(cctx, "worker.cleanup", protocol.WorkerCleanup{MaxAgeSeconds: int64(set.MaxAge / time.Second), Running: retained, WorktreesDir: d.worktreesDir()}); err != nil &&
		!strings.Contains(err.Error(), "unknown frame type") { // an older gateway has no such frame
		d.Logf("worker.cleanup: %v", err)
	}
}

// ---- keeping an eye on workers ----

func (d *Daemon) workerLoop(ctx context.Context) {
	set := d.workerSettings("")
	t := time.NewTicker(set.Tick)
	defer t.Stop()
	c := time.NewTicker(set.Cleanup)
	defer c.Stop()
	d.cleanupWorkers(ctx) // at start
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.C:
			d.cleanupWorkers(ctx)
		case <-t.C:
			d.reconcileOpenCode(ctx)
			d.checkWorkers(ctx)
		}
	}
}

func (d *Daemon) checkWorkers(ctx context.Context) {
	snap := d.SettingsSnapshot()
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
		if w.pid != 0 && !w.stopping && !w.agyStarting && started {
			list = append(list, w)
		}
	}
	d.wmu.Unlock()
	now := time.Now()
	for _, w := range list {
		set := d.workerSettingsWith(snap, w.project)
		if ctx.Err() != nil {
			return
		}
		d.wmu.Lock()
		age, idle, owner := now.Sub(w.startedAt), now.Sub(w.lastActive), w.owner
		harness, native := w.harness, w.nativeID
		sending := w.sendsInFlight > 0
		// A hand-back may run the project's check for up to its timeout. Its
		// own end follows the notice, so age and idleness must not race it.
		checking := w.checking
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
		if d.endFailedWorker(ctx, w) {
			continue
		}
		// Refresh the context and token figures while the worker runs. A
		// harness that cannot say returns an empty result.
		stats := d.workerStats(w)
		grew := d.noteWorkerUsage(w, stats)
		// A session that is busy, a run whose token count grew, or a message
		// that is still on its way is working: it is not idle even when no
		// message has come for a while. This keeps a worker busy on a long
		// task past the idle limit, and keeps a slow message from ending the
		// worker it is meant to keep alive.
		if grew || sending || d.sessionBusy(harness, native) {
			d.keepWorkerActive(w, now)
			idle = 0
		}
		d.checkIdleNotice(ctx, w, now, set.IdleNotice, stats)
		switch {
		case d.gone(w) && !(harness == "agy" && agyTurnFinished(d, w)):
			// The process may have ended after the failure check above. Ask
			// again, so the harness's own error is not lost to the generic note.
			if !d.endFailedWorker(ctx, w) {
				d.beginWorkerEnd(w, protocol.StopExited, nil)
			}
		case checking:
			// The hand-back sends its notice and ends the worker itself.
		case age >= set.MaxAge:
			d.beginWorkerEnd(w, protocol.StopMaxAge, nil)
		case idleLimit >= 0 && idle >= idleLimit:
			// Look again under the worker lock: a message may have started
			// after the snapshot above, and it keeps the worker alive.
			d.beginWorkerEnd(w, protocol.StopIdle, func() bool {
				return w.sendsInFlight == 0 && now.Sub(w.lastActive) >= idleLimit
			})
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
				d.beginWorkerEnd(w, protocol.StopOwnerGone, nil)
			}
		}
	}
}

// handbackFailure is the failure text of a hand-back that ends the run, or ""
// when it did not fail. A failed Codex turn reports the provider error in its
// hand-back report ("The Codex turn failed: ..."), so this is where a usage
// limit reaches worker.stopped (decision 0145). A hand-back that did not fail
// clears an older text.
func handbackFailure(status, report string) string {
	if status != protocol.StatusFailed {
		return ""
	}
	return cleanFailure(report)
}

// endFailedWorker ends a worker whose harness reports a failed run, and says
// whether it did.
func (d *Daemon) endFailedWorker(ctx context.Context, w *worker) bool {
	msg, provider := d.modelFailure(ctx, w)
	if msg == "" {
		return false
	}
	d.wmu.Lock()
	w.failure = msg
	d.wmu.Unlock()
	reason := protocol.StopRunFailed
	if provider {
		reason = protocol.StopModelFails
	}
	d.beginWorkerEnd(w, reason, nil)
	return true
}

// modelFailure asks the harness whether the worker's model call failed. A
// worker that already handed back is left alone: its owner has its answer.
func (d *Daemon) modelFailure(ctx context.Context, w *worker) (string, bool) {
	f, ok := d.launchers[w.harness].(failer)
	if !ok {
		return "", false
	}
	d.wmu.Lock()
	skip := w.doneStatus != "" || w.stopping || w.sessionID == ""
	d.wmu.Unlock()
	if skip {
		return "", false
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return f.failure(cctx, d, w)
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
const recentWindow = protocol.RecentWorkerWindow

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
	sid, owner, harness, model, project, task, title := w.sessionID, w.owner, w.harness, w.model, w.project, w.taskText, w.title
	started, contextTokens, contextLimit, totalTokens := w.startedAt, w.contextTokens, w.contextLimit, w.totalTokens
	cont, fix, asked, used := w.continueCount, w.fixCount, w.difficultyAsked, w.difficultyUsed
	d.wmu.Unlock()
	if sid == "" {
		return
	}
	view := protocol.WorkerView{SessionID: sid, NodeID: d.cfg.NodeID, Hostname: d.cfg.Hostname, Harness: harness,
		Model: model, Project: project, Owner: owner, StartedAt: started.Unix(), Task: task, Title: title, EndedAt: at.Unix(),
		ContextTokens: contextTokens, ContextLimit: contextLimit, Tokens: totalTokens,
		Continue: cont, Fix: fix, DifficultyAsked: asked, DifficultyUsed: used,
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
		// agy may have written its last result while the daemon was down.
		_, agyStreamErr := os.Stat(filepath.Join(d.cfg.StateDir, "workers", r.ID, "agy-stream.json"))
		agyRecoverable := r.Harness == "agy" && r.NativeID != "" && agyStreamErr == nil && (!alive || tok == r.StartToken)
		if (!alive || tok != r.StartToken) && !agyRecoverable {
			if err := d.st.deleteWorkerIdleNotice(r.ID); err != nil {
				d.Logf("worker %s: remove idle notice: %v", r.ID, err)
			}
			r.EndedReason = protocol.StopLost
			if r.DoneStatus == "" {
				lost := &worker{runGeneration: r.RunGeneration, id: r.ID, worktree: r.Worktree, base: r.Base, project: r.Project, startedAt: r.StartedAt, harness: r.Harness, model: r.Model, sessionID: r.SessionID,
					owner: r.Owner, ownerName: r.OwnerName, difficultyAsked: r.DifficultyAsked, difficultyUsed: r.DifficultyUsed, title: r.Title, branch: r.Branch}
				r.Evidence = d.measureWorkerEvidence(context.Background(), lost, nil)
				d.saveUnfinished(lost, protocol.StopLost)
			}
			d.st.saveWorker(r)
			os.RemoveAll(filepath.Join(d.cfg.StateDir, "workers", r.ID))
			d.Logf("worker.stopped worker=%s session=%s project=%s harness=%s reason=%s", r.ID, r.SessionID, r.Project, r.Harness, protocol.StopLost)
			continue
		}
		w := &worker{runGeneration: r.RunGeneration, evidence: r.Evidence, id: r.ID, harness: r.Harness, project: r.Project, path: r.Path, model: r.Model, owner: r.Owner, ownerName: r.OwnerName,
			nativeID: r.NativeID, sessionID: r.SessionID, pid: r.PID, pgid: r.PGID, token: r.StartToken,
			startedAt: r.StartedAt, lastActive: time.Now(), ready: make(chan struct{}),
			dir: filepath.Join(d.cfg.StateDir, "workers", r.ID), mainPath: r.MainPath, worktree: r.Worktree,
			branch: r.Branch, base: r.Base, taskMsg: r.TaskMsg, taskText: r.TaskText, title: r.Title, doneStatus: r.DoneStatus, donePartial: r.DonePartial, ref: r.Ref,
			taskFull:    r.TaskFull,
			permissions: protocol.WorkerPermissionsOrDefault(r.Permissions), continueCount: r.Continue, fixCount: r.Fix,
			continueRun: r.ContinueRun, fixRun: r.FixRun, pendingHandback: r.PendingHandback, pendingPartial: r.PendingPartial,
			taskID: r.TaskID, midrunCount: r.MidrunCount, midrunRun: r.MidrunRun, noticeSinceRequest: r.NoticeSinceRequest, midrunSeen: r.MidrunSeen,
			autoContinue: r.AutoContinue, autoContinueCount: r.AutoContinueCount, autoContinueRun: r.AutoContinueRun,
			difficultyAsked: r.DifficultyAsked, difficultyUsed: r.DifficultyUsed, touches: r.Touches, changeID: r.ChangeID, fixes: r.Fixes}
		w.logPath = filepath.Join(w.dir, "worker.log")
		w.osSandbox = readOSSandboxMarker(w.dir)
		if since, key, report, sent, err := d.st.loadWorkerIdleNotice(w.id); err == nil {
			w.idleNoticeSince, w.idleNoticeKey, w.idleNoticeReport, w.idleNoticeSent = since, key, report, sent
		} else {
			d.Logf("worker %s: load idle notice: %v", w.id, err)
		}
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
				d.goBackground(func() { d.endWorker(w, protocol.StopLost) })
				continue
			}
		}
		d.Logf("worker.adopted worker=%s session=%s project=%s pid=%d", w.id, w.sessionID, w.project, w.pid)
	}
}

// Retain gateway records while a local checkout or branch can be continued.
func (d *Daemon) retainedWorkerSessionIDs() ([]string, error) {
	ids := d.runningSessionIDs()
	rows, err := d.st.workers()
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.SessionID != "" && workerWorktreeRetained(&r) {
			ids = append(ids, r.SessionID)
		}
	}
	return ids, nil
}

// runningSessionIDs lists the gateway session IDs of the workers this daemon
// runs or has adopted.
func (d *Daemon) runningSessionIDs() []string {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	var ids []string
	for _, w := range d.workers {
		if w.sessionID != "" {
			ids = append(ids, w.sessionID)
		}
	}
	sort.Strings(ids)
	return ids
}

// workerCount is how many workers run on this machine.
func (d *Daemon) workerCount() int {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return len(d.workers)
}

func splitTouches(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
