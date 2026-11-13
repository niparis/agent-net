package protocol

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/settings"
)

// Workers (decision 0002): agent sessions that a daemon starts for a caller.

// The default admission values are read from the embedded recipe.
var (
	WorkerCPUHot           = (settings.Snapshot{}).Float("workers.cpu_hot", settings.Where{})
	DefaultWorkerRAM       = (settings.Snapshot{}).Int("workers.cost_default_ram", settings.Where{})
	DefaultWorkerCores     = (settings.Snapshot{}).Float("workers.cost_default_cores", settings.Where{})
	DefaultMemReserveBytes = (settings.Snapshot{}).Int("workers.memory_reserve_min", settings.Where{})
	DefaultMemReserveShare = (settings.Snapshot{}).Float("workers.memory_reserve_share", settings.Where{})
	WorkerCostKeep         = (settings.Snapshot{}).Int("workers.cost_history_limit", settings.Where{})
	DefaultAutoContinues   = (settings.Snapshot{}).Int("workers.auto_continue_max", settings.Where{})
)

const (
	WorkerQueueTick = 15 * time.Second
	QueueIDPrefix   = "q_"
	// StartIDPrefix is the prefix of the start id that an accepted start is
	// answered with, before its queue id or session id is known.
	StartIDPrefix = "a_"
)

// WorkerAutoContinueText is the default reserved continuation message.
var WorkerAutoContinueText = WorkerAutoContinueWithSettings(settings.Snapshot{}, "")

const (
	// WorkerHandbackTool is the code-owned hand-back tool name.
	WorkerHandbackTool  = "agent_handback"
	workerCompleteYes   = "complete: yes"
	workerCompleteNo    = "complete: no"
	workerCompleteFalse = "complete: false"
)

// renderWorkerText keeps protocol tokens outside editable prose. Required slots
// are validated by the settings codec before values reach this renderer.
func renderWorkerText(snap settings.Snapshot, key string, where settings.Where) string {
	mcpTool := "mcp__agent-net__" + WorkerHandbackTool
	return strings.NewReplacer(
		"{{handback}}", WorkerHandbackTool,
		"{{mcp_handback}}", mcpTool,
		"{{search_handback}}", "select:"+mcpTool,
		"{{complete_yes}}", workerCompleteYes,
		"{{complete_no}}", workerCompleteNo,
		"{{complete_false}}", workerCompleteFalse,
	).Replace(snap.String(key, where))
}

func WorkerAutoContinueWithSettings(snap settings.Snapshot, repo string) string {
	return "[agent-net] continue: " + renderWorkerText(snap, "workers.continue_prompt_text", settings.Where{Repo: repo})
}

// WorkerAutoContinueNote says how many automatic continues were sent before a
// worker stopped, for the caller's notice. Empty when there were none.
func WorkerAutoContinueNote(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 automatic continue was sent."
	default:
		return fmt.Sprintf("%d automatic continues were sent.", n)
	}
}

// AutoContinueEnabled tells whether a start asked for automatic continues. An
// absent value (nil, from a caller that does not know the setting) means yes.
func AutoContinueEnabled(p *bool) bool { return p == nil || *p }

// IsQueueID tells whether id names a queued start and not a session.
func IsQueueID(id string) bool { return strings.HasPrefix(id, QueueIDPrefix) }

// IsStartID tells whether id is the start id of an accepted start.
func IsStartID(id string) bool { return strings.HasPrefix(id, StartIDPrefix) }

// Why a worker ended. They are the "reason" of worker.stopped.
const (
	StopIdle       = "idle"           // no message to or from it for the idle time
	StopMaxAge     = "max_age"        // it ran for the longest time allowed
	StopOwnerGone  = "owner_gone"     // its owner ended or stayed offline
	StopRequested  = "stopped"        // agent_worker_stop or `agent-net stop`
	StopExited     = "process_exited" // the harness process ended by itself
	StopLost       = "lost"           // gone while the daemon was not running
	StopStartFails = "start_failed"   // it never became a session
	StopRunFailed  = "run_failed"     // a local or unclassified run failure
	StopModelFails = "model_failed"   // the model call failed (provider, billing or auth error)
	// StopHandback means the worker handed back its report and the daemon
	// ended it, like a Claude Code subagent. The harness session is kept, so
	// a message from the owner starts it again (W10).
	StopHandback = "handback"
)

// WorkerResume is "worker.resume", gateway to the machine: start an ended
// worker again on its own harness session. Only its owner may ask.
type WorkerResume struct {
	Session   string `json:"session"`
	Requester string `json:"requester_session"`
}

// WorkerResumeResult answers "worker.resume". When the machine queued the
// restart, Queued is true and Position and Reason give its place in the queue.
type WorkerResumeResult struct {
	Queued   bool   `json:"queued,omitempty"`
	QueueID  string `json:"queue_id,omitempty"`
	Position int    `json:"position,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// WorkerContinue is "worker.continue", gateway to the machine: start a new
// worker from an ended one. Only the owner of the old worker may ask.
type WorkerContinue struct {
	// StartRef is the reference the gateway tracks this start under (see WorkerLaunch).
	StartRef string   `json:"start_ref,omitempty"`
	Touches  []string `json:"touches,omitempty"`
	FromCTO  bool     `json:"sender_cto,omitempty"`
	// Session is the old worker's session ID.
	Session   string `json:"session"`
	Requester string `json:"requester_session"`
	TaskID    string `json:"task_id,omitempty"`
	// Harness and Model are empty to keep the old worker's.
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	// Assessment is the old run's judge result, kept when the brief does not
	// change. The machine that starts the new worker records it with a new
	// run ID, or assesses the changed brief itself.
	Assessment *WorkerAssessment `json:"assessment,omitempty"`
	// Task is the extra text of the caller, added after the old first task.
	Task string `json:"task,omitempty"`
	// Title replaces the old worker's title when it is set.
	Title string `json:"title,omitempty"`
}

// WorkerContinued answers "worker.continue".
type WorkerContinued struct {
	WorkerLaunched
	Project string `json:"project"`
	Path    string `json:"path"`
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	// Title is the new worker's short title.
	Title string `json:"title,omitempty"`
	// Track and Playbooks are the gateway's record and the fresh notes for the
	// chosen harness and model (decision 0022).
	Track     *TrackRecord   `json:"track,omitempty"`
	Playbooks []PlaybookNote `json:"playbooks,omitempty"`
	// Assessment is the judge result this run was started with.
	Assessment *WorkerAssessment `json:"assessment,omitempty"`
}

// ContinueIntro starts the task of a worker that continues an ended one.
// where says what the earlier worker left: "branch" (its unfinished changes
// are the last commit in the new worktree), "folder" (it worked in the project
// folder) or "" (nothing).
func ContinueIntro(reason, where string) string {
	return ContinueIntroWithSettings(reason, where, settings.Snapshot{}, "")
}

func ContinueIntroWithSettings(reason, where string, snap settings.Snapshot, repo string) string {
	at := settings.Where{Repo: repo}
	head := snap.String("workers.continue_intro", at) + " (" + NeutralizeMarkers(reason) + "). "
	switch where {
	case "branch":
		return head + snap.String("workers.continue_intro_branch", at)
	case "folder":
		return head + snap.String("workers.continue_intro_folder", at)
	default:
		return head + snap.String("workers.continue_intro_empty", at)
	}
}

// WorkerCleanup is "worker.cleanup", machine to gateway: drop the worker
// records of this machine that are offline and started longer ago than
// MaxAge. The daemon sends it at start and periodically (W7). Running lists
// the session IDs of the workers the machine runs or has adopted: the gateway
// never drops those records, whatever their age or state.
// Statuses of a WorkerStartReport.
const (
	StartReportNotStarted = "not started"
	StartReportQueued     = "queued"
)

// WorkerStartReportText is the notice for a start that was accepted without
// an answer and then ended without a running worker, or was queued.
func WorkerStartReportText(status, project, harness, startID, note string) string {
	return "[agent-net worker notice]\nstatus: " + status + "\nnote: " + note + "\nstart: " + startID +
		"\nworker: none (" + harness + "), project " + project + "\n"
}

// WorkerAcceptedStartText is the notice for a start that was accepted without
// an answer and then became a worker. status is "started" or "starting".
func WorkerAcceptedStartText(startID, status, session, harness, model, project, host, worktree, branch, ownerLine, why string) string {
	note := "the accepted start has begun; the task has been sent to the worker"
	if status == "starting" {
		note = "the accepted start has begun; the worker is being set up and gets its task when it is ready"
	}
	b := "[agent-net worker notice]\nstatus: " + status + "\nnote: " + note + "\nstart: " + startID + "\nworker: " + session + " (" + harness
	if model != "" {
		b += ", " + model
	}
	b += "), project " + project + " on " + host
	if worktree != "" {
		b += "\nworktree: " + worktree
	}
	if branch != "" {
		b += "\nbranch: " + branch
	}
	if ownerLine != "" {
		b += "\n" + ownerLine
	}
	if why != "" {
		b += "\nwhy: " + why
		b += "\nfull: agent_workers_find {worker, detail:full}"
	}
	return b + "\n"
}

// WorkerNotStartedText is the text of the notice for a queued start that did not
// become a worker.
func WorkerNotStartedText(project, harness, why string) string {
	return "[agent-net worker notice]\nstatus: not started\nnote: " + why + "\nworker: none (" + harness + "), project " + project + "\n"
}

// WorkerStartedText is the notice for a queued start that has become a worker.
func WorkerStartedText(session, harness, model, project, host string, waited time.Duration, why string, title ...string) string {
	b := "[agent-net worker notice]\nstatus: started\nnote: the queued start has begun; the task is being sent to the worker\nworker: " +
		session + " (" + harness
	if model != "" {
		b += ", " + model
	}
	b += "), project " + project + " on " + host
	if len(title) > 0 && strings.TrimSpace(title[0]) != "" {
		b += " · " + strings.TrimSpace(title[0])
	}
	b += "\nmodel: " + ParseModelSelection(harness, "", model, "").Display()
	if why != "" {
		b += "\nwhy: " + why
		b += "\nfull: agent_workers_find {worker, detail:full}"
	}
	b += "\nqueue wait: " + waited.Round(time.Second).String() + "\n"
	return b
}

type WorkerCleanup struct {
	MaxAgeSeconds int64 `json:"max_age_seconds"`
	// WorktreesDir is the folder of this machine where its workers' git
	// worktrees live. The gateway also deletes this machine's offline sessions
	// that are not worker records and work inside it (ghosts of workers that
	// a user's own service registered). An older daemon sends none.
	WorktreesDir string `json:"worktrees_dir,omitempty"`
	// Running lists the session IDs of the workers the machine runs or has
	// adopted; the gateway never drops those records.
	Running []string `json:"running,omitempty"`
}

// WorkerInfo is the worker part of a session. The daemon that started the
// worker reports it; the gateway keeps it. A session with it is a worker, and
// a worker cannot start workers.
type WorkerInfo struct {
	// RunGeneration identifies this start or resume; zero is an older daemon.
	RunGeneration int               `json:"run_generation,omitempty"`
	Assessment    *WorkerAssessment `json:"assessment,omitempty"`
	// ID is the worker's local ID on its machine.
	ID string `json:"id"`
	// Effort is the effort the worker was started with. A harness may report
	// its live model without it (Claude does), so lists fall back to this.
	Effort string `json:"effort,omitempty"`
	// StartRef is the reference of the start this worker belongs to.
	StartRef string `json:"start_ref,omitempty"`
	// Owner is the session that started the worker (the owner's own
	// pseudo-session for a start from the command line or the TUI).
	Owner string `json:"owner"`
	// Project is the project name it was started in.
	Project   string `json:"project,omitempty"`
	StartedAt int64  `json:"started_at"`
	// Task is the first line of the task its owner gave it, cut to about 120
	// characters. Empty when the machine that started it did not say.
	Task string `json:"task,omitempty"`
	// Title is one short line naming the worker: the one the caller gave, or
	// a line derived from the task. Empty for a worker started by an older
	// daemon or gateway.
	Title string `json:"title,omitempty"`
	// ContextTokens is how many tokens the model saw on the worker's last
	// step: input plus cache reads and cache writes of the latest assistant
	// message or turn. 0 when the harness cannot say.
	ContextTokens int64 `json:"context_tokens,omitempty"`
	// ContextLimit is the context window of the worker's model, in tokens.
	// 0 when the harness cannot say.
	ContextLimit int64 `json:"context_limit,omitempty"`
	// Tokens is the cumulative input plus output plus reasoning of the run,
	// without cache reads. 0 when the harness cannot say.
	Tokens int64 `json:"tokens,omitempty"`
	// Usage is the same run by kind of token, cache reads and writes included.
	// The gateway keeps it with the run to price it (decision 0160).
	Usage *RunTokens `json:"usage,omitempty"`
	// Permissions is the level the worker was started with (decision 0020):
	// sandboxed, no-network or read-only. Codex, Antigravity and Claude
	// workers apply it with their own sandbox.
	Permissions string `json:"permissions,omitempty"`
	// OSSandbox is the OS wrapper the worker runs in ("bubblewrap"), or ""
	// (docs/design/worker-os-sandbox.md).
	OSSandbox string `json:"os_sandbox,omitempty"`
	// Continue counts the messages the caller sent after a hand-back with
	// complete:no; Fix counts those after a hand-back with complete:yes.
	Continue int `json:"continue,omitempty"`
	Fix      int `json:"fix,omitempty"`
	// AutoContinue counts the continues the daemon sent itself after the
	// worker stopped early. They are not caller rework.
	AutoContinue int `json:"auto_continue,omitempty"`
	// DifficultyAsked is the difficulty the caller asked for (empty when it
	// named none); DifficultyUsed is the level the chooser used.
	DifficultyAsked string   `json:"difficulty_asked,omitempty"`
	DifficultyUsed  string   `json:"difficulty_used,omitempty"`
	Touches         []string `json:"touches,omitempty"`
	// Struggle is the sign count of the worker's latest run, computed at its
	// end from the run's own tool record (decision 0133).
	Struggle *Struggle `json:"struggle,omitempty"`
	// Cause is the recorded reason for the latest run's failure or rework,
	// one of the Cause constants (decision 0145). Empty when none is known.
	Cause     string   `json:"cause,omitempty"`
	NotFamily []string `json:"not_family,omitempty"`
}

// Frames for workers. The caller's daemon sends "worker.start" and
// "worker.stop" to the gateway. The gateway checks them and sends "worker.launch"
// and "worker.kill" to the machine that runs the worker. That machine's daemon
// sends "worker.stopped" when a worker ends.
type (
	// WorkerStartFrame is "worker.start".
	WorkerStartFrame struct {
		FromCTO bool `json:"sender_cto,omitempty"`
		// RequireReadOnly restricts automatic background tasks to a harness
		// and daemon that enforce read-only project access.
		RequireReadOnly bool `json:"require_read_only,omitempty"`
		WorkerStartRequest
		// Owner is the caller's session ID. The gateway checks it belongs to
		// the sending node.
		Owner string `json:"owner_session"`
		// CallerProject is true when the caller's daemon filled the project
		// from the caller's own session. If the project is on several
		// machines, the gateway then prefers the caller's machine, or the one
		// machine that has the harness.
		CallerProject bool `json:"caller_project,omitempty"`
		// CallerPath is the main working tree of the caller's own folder,
		// set with CallerProject. The gateway matches it exactly on the
		// caller's machine before it falls back to the name.
		CallerPath string `json:"caller_path,omitempty"`
		// Assessment is the difficulty judge's result for this brief, made
		// before the gateway chooses a model. Its label is the level. The
		// machine that starts the worker stores it with the new session.
		Assessment *WorkerAssessment `json:"assessment,omitempty"`
		// Level is an internal caller's fixed level, used when there is no
		// assessment (for example the read-only phrasing worker). A caller's
		// Difficulty field is ignored.
		Level string `json:"level,omitempty"`
		// Precheck is set by the gateway for "worker.precheck": the checks that
		// need no judge run, nothing starts. A caller never sets it.
		Precheck bool `json:"-"`
	}
	// WorkerStartReport is "worker.start_report", machine to gateway: the end
	// of a start the machine had accepted without an answer (the judge and the
	// launch ran on in the background). The gateway tells the owner, which must
	// be a session of the sending machine, with a notice from agent-net.
	WorkerStartReport struct {
		Owner   string `json:"owner_session"`
		Project string `json:"project"`
		Harness string `json:"harness"`
		// Status is StartReportNotStarted or StartReportQueued.
		Status string `json:"status"`
		// Note is the reason, or the queue facts, as one short line.
		Note string `json:"note"`
		// ID is the start id the caller was given; it makes the notice unique.
		ID string `json:"start_id"`
	}
	// WorkerStartAnswer is the gateway's answer to "worker.start".
	WorkerStartAnswer struct {
		// StartRef lets the gateway recognize its own remote task relay.
		StartRef  string `json:"start_ref,omitempty"`
		Starting  bool   `json:"starting,omitempty"`
		SessionID string `json:"session_id"`
		NodeID    string `json:"node_id"`
		Hostname  string `json:"hostname"`
		Project   string `json:"project"`
		Path      string `json:"path"`
		Harness   string `json:"harness"`
		Model     string `json:"model,omitempty"`
		Provider  string `json:"provider,omitempty"`
		Effort    string `json:"effort,omitempty"`
		// ModelNote says how a loose model name was resolved, for example
		// "deepseek 4.1 -> opencode-go/deepseek-v4.1-flash". Empty when the
		// name was used as it was.
		ModelNote string `json:"model_note,omitempty"`
		// Why is the short explanation for the chosen model.
		Why string `json:"why,omitempty"`
		// Worktree and Branch are set when the worker got its own worktree.
		Worktree string `json:"worktree,omitempty"`
		Branch   string `json:"branch,omitempty"`
		// Base is the branch the worktree started from, when the start named
		// one. It lets the brief say when that is another worker's branch.
		Base string `json:"base,omitempty"`
		// ProjectRules is the text of the project's .agent-net/worker.md.
		ProjectRules string `json:"project_rules,omitempty"`
		// Task is the task text to send, set for a continued worker (it holds
		// the old worker's first task). Empty means the caller's own task.
		Task string `json:"task,omitempty"`
		// Title is the worker's short title.
		Title string `json:"title,omitempty"`
		// Queued is true when the machine has no room now: the start waits
		// in its queue, SessionID is empty, and QueueID names the start. A
		// notice follows when it starts.
		Queued   bool   `json:"queued,omitempty"`
		QueueID  string `json:"queue_id,omitempty"`
		Position int    `json:"position,omitempty"`
		// Reason says why there is no room, or what happened in the queue.
		Reason string `json:"reason,omitempty"`
		// MachineNote says why the gateway picked this machine when the
		// project is on several.
		MachineNote string `json:"machine_note,omitempty"`
		// Hint is one line about a defect this start may fix (see
		// WorkerStartResult.Hint).
		Hint string `json:"hint,omitempty"`
		// Phases is how long the gateway spent on each part of the start, for
		// the daemon's slow log ("picker=12 launch=40", milliseconds).
		Phases string `json:"phases,omitempty"`
		// Permissions is the worker permission level of the project.
		Permissions string `json:"permissions,omitempty"`
		// OSSandbox is the OS wrapper the worker runs in ("bubblewrap"), or ""
		// when it runs without one.
		OSSandbox string `json:"os_sandbox,omitempty"`
		// Track and Playbooks are the gateway's record for the chosen harness
		// and model, and the fresh notes about it (decision 0022).
		Track     *TrackRecord   `json:"track,omitempty"`
		Playbooks []PlaybookNote `json:"playbooks,omitempty"`
		// Checked means the start was mode "check": the overlap check ran and
		// no worker started.
		Checked bool `json:"checked,omitempty"`
		// BriefFailures lists the overlaps that refuse a brief, one per line
		// when it is shown. Empty means the brief passed.
		BriefFailures []string `json:"brief_failures,omitempty"`
	}
	// WorkerLaunch is "worker.launch", gateway to the machine.
	WorkerLaunch struct {
		// StartRef is the reference the gateway tracks this start under. A
		// queued start uses it as its queue ID, and a machine names it when it
		// reports that the start never became a worker.
		StartRef        string `json:"start_ref,omitempty"`
		FromCTO         bool   `json:"sender_cto,omitempty"`
		RequireReadOnly bool   `json:"require_read_only,omitempty"`
		Project         string `json:"project"`
		Path            string `json:"path"`
		Harness         string `json:"harness"`
		Model           string `json:"model,omitempty"`
		Provider        string `json:"provider,omitempty"`
		Effort          string `json:"effort,omitempty"`
		Owner           string `json:"owner_session"`
		// OwnerName is the name the gateway knows for the caller, for the
		// worker's commit trailers. Empty when the record is gone.
		OwnerName string `json:"owner_name,omitempty"`
		// Permissions is the project's worker level ("" means sandboxed).
		Permissions string `json:"permissions,omitempty"`
		// Task is what the worker should do. The machine that starts it uses
		// the first line as the worker's name in a status view.
		Task string `json:"task,omitempty"`
		// Title is the worker's short title. Empty means derive it from Task.
		Title string `json:"title,omitempty"`
		// Fixes is the shipped change this start says it fixes. The machine
		// keeps it with the worker so its hand-back does not hint at the same
		// defect again. Empty when the start named none.
		Fixes string `json:"fixes,omitempty"`
		// Isolation is "" (a worktree if the folder is a repository),
		// IsolationWorktree or IsolationNone.
		Isolation string `json:"isolation,omitempty"`
		Base      string `json:"base,omitempty"`
		// RecordTask is what the machine keeps as the worker's first task.
		// Empty means Task.
		RecordTask string `json:"record_task,omitempty"`
		// TaskID and ContinuationPartial carry a continued task through the
		// admission queue to the new worker's first run.
		TaskID              string `json:"task_id,omitempty"`
		ContinuationPartial bool   `json:"continuation_partial,omitempty"`
		// ChangeID is the delivery change a continue carries to the new
		// worker, so its hand-back moves the same change to the new branch.
		// Only the machine's own continue sets it; it is never read from a
		// frame.
		ChangeID string `json:"-"`
		// DifficultyAsked and DifficultyUsed are kept on the worker record for
		// the difficulty calibration. Both are the difficulty judge's label
		// (or normal on a fallback). Touches is recorded but never changes
		// the level.
		DifficultyAsked string   `json:"difficulty_asked,omitempty"`
		DifficultyUsed  string   `json:"difficulty_used,omitempty"`
		Touches         []string `json:"touches,omitempty"`
		// Assessment is the judge result made before the start. The machine
		// that starts the worker stores it with the new session.
		Assessment *WorkerAssessment `json:"assessment,omitempty"`
		// AutoContinue turns the daemon's own continues for a worker that
		// stopped early on or off (default on).
		AutoContinue *bool `json:"auto_continue,omitempty"`
		// MCPServers is the worker start's "approve" or "disable" for the
		// .mcp.json servers of a Claude worker's folder.
		MCPServers string `json:"mcp_servers,omitempty"`
		// Why is the short explanation for the chosen model.
		Why string `json:"why,omitempty"`
		// NotFamily contains model families excluded by the caller.
		NotFamily []string `json:"not_family,omitempty"`
	}
	// WorkerLaunched answers "worker.launch". When the machine queued the
	// start, Queued is true and SessionID is empty.
	WorkerLaunched struct {
		Starting  bool   `json:"starting,omitempty"`
		SessionID string `json:"session_id"`
		WorkerID  string `json:"worker_id"`
		Worktree  string `json:"worktree,omitempty"`
		Branch    string `json:"branch,omitempty"`
		// ProjectRules is the text of the project's .agent-net/worker.md.
		ProjectRules string `json:"project_rules,omitempty"`
		// Task is the task text to send, for a continued worker.
		Task     string `json:"task,omitempty"`
		Queued   bool   `json:"queued,omitempty"`
		QueueID  string `json:"queue_id,omitempty"`
		Position int    `json:"position,omitempty"`
		Reason   string `json:"reason,omitempty"`
		// OSSandbox is the OS wrapper the worker runs in ("bubblewrap"), or "".
		OSSandbox string `json:"os_sandbox,omitempty"`
	}
	// WorkerNotStarted is "worker.not_started", machine to gateway: a queued
	// start ended without a worker. The gateway records an event for the feed
	// and tells the owner with a notice from the reserved agent-net sender, so
	// the notice never carries the owner's label.
	WorkerNotStarted struct {
		// Ref names one of this machine's starts that the gateway tracks (the
		// StartRef it was given). The gateway takes the owner, project and
		// harness of the notice from its own record of that start and ignores
		// the fields below. Without a Ref (an older machine, which sends its
		// own notice) the gateway only records the event.
		Ref         string `json:"ref,omitempty"`
		Owner       string `json:"owner_session"`
		Project     string `json:"project"`
		Harness     string `json:"harness"`
		Reason      string `json:"reason"`
		Permissions string `json:"permissions,omitempty"`
	}
	// WorkerQueuedStart is "worker.queued_start", machine to gateway: a queued
	// start has become a worker. The gateway passes it to the owner's machine
	// as "worker.task", and that machine sends the task, because only the
	// owner's machine may send as the owner.
	WorkerQueuedStart struct {
		// Ref is the StartRef of the start this task belongs to.
		Ref         string `json:"ref,omitempty"`
		FromCTO     bool   `json:"sender_cto,omitempty"`
		Permissions string `json:"permissions,omitempty"`
		Owner       string `json:"owner_session"`
		SessionID   string `json:"session_id"`
		Harness     string `json:"harness"`
		Project     string `json:"project,omitempty"`
		Task        string `json:"task"`
		Title       string `json:"title,omitempty"`
		Worktree    string `json:"worktree,omitempty"`
		Branch      string `json:"branch,omitempty"`
		// Base is the branch the worktree started from, when the start named
		// one, for the brief's line about another worker's branch.
		Base string `json:"base,omitempty"`
		// ProjectRules is the project's .agent-net/worker.md text, for the brief.
		ProjectRules string `json:"project_rules,omitempty"`
		// OSSandbox is the OS wrapper the worker runs in, for the brief.
		OSSandbox string `json:"os_sandbox,omitempty"`
	}
	// WorkerStopFrame is "worker.stop".
	WorkerStopFrame struct {
		Session   string `json:"session"`
		Requester string `json:"requester_session"`
	}
	// WorkerKill is "worker.kill", gateway to the machine.
	WorkerKill struct {
		Session string `json:"session"`
		// Requester is the session that asked; AsOwner is true when it is the
		// person at a command line. Both only matter for a queued start,
		// which only its owner or the person may cancel.
		Requester string `json:"requester_session,omitempty"`
		AsOwner   bool   `json:"as_owner,omitempty"`
	}
	// WorkerCostQuery is "worker.cost", machine to gateway: the cost of a
	// run's tokens at API prices (decision 0160).
	WorkerCostQuery struct {
		Model  string    `json:"model"`
		Tokens RunTokens `json:"tokens"`
	}
	// WorkerCostReply answers it. Known is false when the gateway has no price
	// list. PriceKnown is false when the model has no listed price and the
	// middle price was used.
	WorkerCostReply struct {
		Cost       float64 `json:"cost"`
		Known      bool    `json:"known"`
		PriceKnown bool    `json:"price_known"`
	}
	// WorkerStopped is "worker.stopped", machine to gateway.
	WorkerStopped struct {
		ProofUnavailable string          `json:"proof_unavailable,omitempty"`
		RunGeneration    int             `json:"run_generation,omitempty"`
		Evidence         *WorkerEvidence `json:"evidence,omitempty"`
		SessionID        string          `json:"session_id"`
		WorkerID         string          `json:"worker_id"`
		Project          string          `json:"project"`
		Harness          string          `json:"harness"`
		Reason           string          `json:"reason"`
		// Failure is the cleaned error text of the run: a failed model call, or
		// the report of a failed hand-back (a failed Codex turn reports its
		// provider error there). It is used to remember provider auth and quota
		// failures and to record the run's cause.
		Failure string `json:"failure,omitempty"`
		// Model is the model the worker ran, as the gateway resolved it. The
		// gateway uses it with Reason model_failed to rank the model last for a
		// while (ModelFailureDays), and with Status done to rank it first
		// (ModelSuccessDays).
		Model string `json:"model,omitempty"`
		// Status is the hand-back status (done, blocked, failed), empty when the
		// worker ended without a hand-back. It feeds the track record.
		Status string `json:"status,omitempty"`
		// Complete is false when the hand-back reported partial work. Nil for a
		// worker that did not hand back, or an older daemon.
		Complete *bool `json:"complete,omitempty"`
		// StartedAt is when the run began, in unix seconds. The gateway falls
		// back to the worker session's start when an older daemon sends none.
		StartedAt int64 `json:"started_at,omitempty"`
		// Continue and Fix are the worker's counts after hand-backs, over its
		// whole record. ContinueRun and FixRun are the part counted during
		// this run. AutoContinue and AutoContinueRun are the daemon's own
		// continues, which cost the caller no rework. An older daemon sends
		// none of them.
		Continue        int    `json:"continue,omitempty"`
		Fix             int    `json:"fix,omitempty"`
		ContinueRun     int    `json:"continue_run,omitempty"`
		FixRun          int    `json:"fix_run,omitempty"`
		AutoContinue    int    `json:"auto_continue,omitempty"`
		AutoContinueRun int    `json:"auto_continue_run,omitempty"`
		TaskID          string `json:"task_id,omitempty"`
		MidrunRun       int    `json:"midrun_run,omitempty"`
		// DifficultyAsked and DifficultyUsed are the difficulty the caller
		// asked for and the level the chooser used.
		DifficultyAsked string   `json:"difficulty_asked,omitempty"`
		DifficultyUsed  string   `json:"difficulty_used,omitempty"`
		Touches         []string `json:"touches,omitempty"`
		// Struggle counts the signs that this run fought its environment or
		// its instructions, from the run's own tool record (decision 0133).
		Struggle *Struggle `json:"struggle,omitempty"`
	}
)

// WorkerHarnesses are the harnesses a worker can run (decision 0002, W4).
// The first one is the default. Decision 0008 has how each is started.
var WorkerHarnesses = []string{"opencode", "claude", "pi", "codex", "agy"}

// KnownHarnesses are the harness names agent-net knows.
var KnownHarnesses = []string{"opencode", "claude", "pi", "codex", "agy"}

// HarnessKnown tells whether agent-net knows this harness name.
func HarnessKnown(h string) bool {
	for _, s := range KnownHarnesses {
		if s == h {
			return true
		}
	}
	return false
}

// WorkerHarnessSupported tells whether a worker can run this harness.
func WorkerHarnessSupported(h string) bool {
	for _, s := range WorkerHarnesses {
		if s == h {
			return true
		}
	}
	return false
}

// WorkerReadOnlySupported reports which worker adapters enforce read-only
// access. Other adapters currently treat permissions as guidance only.
func WorkerReadOnlySupported(h string) bool { return h == "codex" || h == "agy" }

// The default briefs remain available to callers that do not have a snapshot.
var (
	WorkerPreamble      = workerPreamble("", settings.Snapshot{}, "")
	WorkerPreambleCodex = workerPreamble("codex", settings.Snapshot{}, "")
	WorkerClaudeNote    = "\n- " + renderWorkerText(settings.Snapshot{}, "workers.brief_claude", settings.Where{})
)

func workerPreamble(harness string, snap settings.Snapshot, repo string) string {
	return workerPreambleBar(harness, snap, repo, nil)
}

// workerPreambleBar is workerPreamble with the project's bar. The bar follows
// the shared rules, so a worker reads the project's own rules last.
func workerPreambleBar(harness string, snap settings.Snapshot, repo string, bar []BarRule) string {
	where := settings.Where{Repo: repo}
	line := func(key string) string { return renderWorkerText(snap, key, where) }
	b := "[agent-net worker brief] " + line("workers.brief_preamble")
	for _, key := range []string{"workers.brief_task", "workers.brief_no_delegation"} {
		b += "\n- " + line(key)
	}
	if testStepElsewhere(snap, repo) {
		b += "\n- " + line("workers.brief_tests")
	}
	keys := []string{"workers.brief_handback", "workers.brief_handback_partial", "workers.brief_blocked", "workers.brief_approval"}
	if harness == "codex" {
		keys = []string{"workers.brief_codex_tools", "workers.brief_codex_final", "workers.brief_codex_complete", "workers.brief_codex_blocked", "workers.brief_approval"}
	}
	for _, key := range keys {
		b += "\n- " + line(key)
	}
	for _, rule := range []string{
		"Reuse the package's existing test helpers; add new test infrastructure only if none exists.",
		"Put shared setup in one helper, not copied into each test.",
		"One test per behaviour; no cases for behaviour the task does not change.",
		`Do not prove "nothing happened" with sleeps or short timeouts; assert on state.`,
		"Comments say why, never restate the code.",
		"Put a check in one place, at the entry point, not repeated in every layer.",
	} {
		b += "\n- " + rule
	}
	if section := BarBriefSection(bar); section != "" {
		b += "\n" + section
	}
	b += "\nThe project's own AGENTS.md wins where it conflicts."
	// The Claude line comes last, so the Claude brief starts with the default
	// brief (WorkerPreamble) and only adds to it.
	if harness == "claude" {
		b += "\n- " + line("workers.brief_claude")
	}
	return b
}

// testStepElsewhere tells whether the project's test step belongs to someone
// other than the worker's caller: the merger, a node or GitHub. It reads the
// same pipeline as the step tasks, and only the test step's owner decides it.
// tests_before_merge does not: the test step is in the pipeline either way.
// Then the brief says to run only the worker's own tests, because that owner
// runs the suites. A caller-owned step, or no project, gets no line.
func testStepElsewhere(snap settings.Snapshot, repo string) bool {
	if repo == "" {
		return false
	}
	p, err := PipelineFromSettings(snap, repo)
	if err != nil {
		return false
	}
	test, ok := p.Find(StepRef{Step: StepTest})
	return ok && test.Owner.Kind != OwnerCaller
}

// WorkerBrief is the structured task of a worker start (decision 0146). Text
// renders it in one fixed order, so a worker sees the same shape from every
// caller in every project.
type WorkerBrief struct {
	Problem    string
	Evidence   []string
	Done       []string
	Hypothesis string
	Scope      string
	Notes      string
}

// WorkerBriefFromStart reads the structured fields of a start. Task is the
// notes.
func WorkerBriefFromStart(p WorkerStartRequest) WorkerBrief {
	return WorkerBrief{Problem: p.Problem, Evidence: p.Evidence, Done: p.Done,
		Hypothesis: p.Hypothesis, Scope: p.Scope, Notes: p.Task}
}

// BriefText is the task text of a start: the structured brief when it has any
// structured field, else the old Task text unchanged.
func (p WorkerStartRequest) BriefText() string { return WorkerBriefFromStart(p).Text() }

// Structured tells whether the brief has any field beyond the old Task.
func (b WorkerBrief) Structured() bool {
	return strings.TrimSpace(b.Problem) != "" || strings.TrimSpace(b.Hypothesis) != "" ||
		strings.TrimSpace(b.Scope) != "" || len(cleanBriefLines(b.Evidence)) > 0 || len(cleanBriefLines(b.Done)) > 0
}

// Text renders the brief in the fixed order Problem, Evidence, Done,
// Hypothesis, Scope, Notes. Every non-empty section is its own block, so a
// worker can see what was asked, what was known and what counts as done.
// A brief with no structured field is the old Task text, unchanged.
func (b WorkerBrief) Text() string {
	if !b.Structured() {
		return strings.TrimSpace(b.Notes)
	}
	var parts []string
	add := func(heading, text string) {
		if text = strings.TrimSpace(text); text != "" {
			parts = append(parts, heading+":\n"+text)
		}
	}
	addList := func(heading string, lines []string) {
		lines = cleanBriefLines(lines)
		if len(lines) == 0 {
			return
		}
		var s strings.Builder
		s.WriteString(heading + ":")
		for _, line := range lines {
			s.WriteString("\n- " + line)
		}
		parts = append(parts, s.String())
	}
	add("Problem", b.Problem)
	addList("Evidence", b.Evidence)
	addList("Done", b.Done)
	add("Hypothesis (yours to drop if wrong)", b.Hypothesis)
	add("Scope", b.Scope)
	add("Notes", b.Notes)
	return strings.Join(parts, "\n\n")
}

// cleanBriefLines trims each line and drops empty ones.
func cleanBriefLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// WorkerTaskBody builds the body of the task message for an OpenCode worker.
func WorkerTaskBody(task, worktree, branch string) string {
	return WorkerTaskBodyFor("", task, worktree, branch)
}

// WorkerTaskBodyFor builds the body of the task message: the brief of the
// harness, a line about the worktree if there is one, then the task.
func WorkerTaskBodyFor(harness, task, worktree, branch string) string {
	return WorkerTaskBodyWithRules(harness, task, worktree, branch, "")
}

func WorkerTaskBodyWithRules(harness, task, worktree, branch, rules string) string {
	return WorkerTaskBodyWithPermissions(harness, task, worktree, branch, rules, "")
}

// WorkerPermissionsText describes the effective project level of a worker
// that runs without the OS sandbox.
func WorkerPermissionsText(level, harness string) string {
	return WorkerPermissionsLine(level, harness, "")
}

// WorkerEnforcer names what enforces a worker's level: the OS sandbox the
// worker runs in (osSandbox, such as "bubblewrap"), the harness's own sandbox
// ("codex", or "agy" except for no-network), or "" when nothing does yet.
func WorkerEnforcer(level, harness, osSandbox string) string {
	switch {
	case osSandbox != "":
		return osSandbox
	case harness == "codex", harness == "claude", harness == "agy" && WorkerPermissionsOrDefault(level) != WorkersNoNetwork:
		return harness
	}
	return ""
}

// WorkerBaseNote is the line a brief adds when the worker's base is another
// worker's branch: its own worktree already holds that branch's head, so it
// has no reason to go to that branch's worktree and every reason not to.
func WorkerBaseNote(base, worktree string, snap settings.Snapshot) string {
	if base == "" || worktree == "" {
		return ""
	}
	prefix := snap.String("delivery.worker_branch_prefix", settings.Where{})
	if !strings.HasPrefix(base, prefix+"w_") {
		return ""
	}
	return "Your worktree already starts at the head of " + base + " (another worker's branch); edit and commit only in " + worktree + ", never in another worker's worktree."
}

// workerHarnesses are the harnesses a worker can run in, by the names the
// owner and the tools use.
var workerHarnesses = []string{"claude", "codex", "agy", "opencode", "pi"}

// WorkerEnforcedBy names the harnesses whose own sandbox enforces a level, for
// example "claude, codex and agy". It asks WorkerEnforcer, the place that
// decides it, so the names follow the launch code.
func WorkerEnforcedBy(level string) string {
	var names []string
	for _, h := range workerHarnesses {
		if WorkerEnforcer(level, h, "") != "" {
			names = append(names, h)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// WorkerPermissionsLine describes the effective project level, and what
// enforces it (docs/design/worker-os-sandbox.md). A harness that does not
// enforce the level has the names of the harnesses that do.
func WorkerPermissionsLine(level, harness, osSandbox string) string {
	level = WorkerPermissionsOrDefault(level)
	detail := "writes only in its worktree, network on"
	switch level {
	case WorkersNoNetwork:
		detail = "writes only in its worktree, network off"
	case WorkersReadOnly:
		detail = "no writes"
	}
	text := "permissions: " + level + " (" + detail + ")"
	switch {
	case osSandbox != "":
		text += " (sandboxed by " + osSandbox + ")"
	case WorkerEnforcer(level, harness, "") == "":
		text += " (enforced for " + WorkerEnforcedBy(level) + " only for now)"
	}
	return text
}

func WorkerTaskBodyWithPermissions(harness, task, worktree, branch, rules, level string) string {
	return WorkerTaskBodyWithCharter(harness, task, worktree, branch, rules, "", level)
}

// WorkerTaskBodyWithCharter is WorkerTaskBodyWithPermissions with the charter
// of the worker's node. The charter section follows the project rules and
// comes before the task (decision 0022).
func WorkerTaskBodyWithCharter(harness, task, worktree, branch, rules, charter, level string) string {
	return WorkerTaskBodyWithSettings(harness, task, worktree, branch, rules, charter, level, settings.Snapshot{})
}

func WorkerTaskBodyWithSettings(harness, task, worktree, branch, rules, charter, level string, snap settings.Snapshot) string {
	return WorkerTaskBodyWithSettingsForRepo(harness, task, worktree, branch, rules, charter, level, snap, "")
}

// WorkerTaskBodyWithSettingsForRepo resolves advice for the worker's repo.
func WorkerTaskBodyWithSettingsForRepo(harness, task, worktree, branch, rules, charter, level string, snap settings.Snapshot, repo string) string {
	return WorkerTaskBodyOS(harness, task, worktree, branch, rules, charter, level, "", snap, repo)
}

// WorkerTaskBodyOS is WorkerTaskBodyWithSettingsForRepo for a worker that may
// run in the OS sandbox: osSandbox names the wrapper, "" when there is none.
func WorkerTaskBodyOS(harness, task, worktree, branch, rules, charter, level, osSandbox string, snap settings.Snapshot, repo string) string {
	return WorkerTaskBodyBriefBar(harness, WorkerBrief{Notes: task}, worktree, branch, "", rules, charter, level, osSandbox, snap, repo, nil)
}

// WorkerTaskBodyOSBase is WorkerTaskBodyOS with the branch the worker was
// based on. When that branch is another worker's, the brief says the worker's
// own worktree already starts at its head and that only that worktree is the
// worker's to edit.
func WorkerTaskBodyOSBase(harness, task, worktree, branch, base, rules, charter, level, osSandbox string, snap settings.Snapshot, repo string) string {
	return WorkerTaskBodyBriefBar(harness, WorkerBrief{Notes: task}, worktree, branch, base, rules, charter, level, osSandbox, snap, repo, nil)
}

// WorkerTaskBodyBar is WorkerTaskBodyOS with the project's bar in the header.
func WorkerTaskBodyBar(harness, task, worktree, branch, rules, charter, level, osSandbox string, snap settings.Snapshot, repo string, bar []BarRule) string {
	return WorkerTaskBodyBriefBar(harness, WorkerBrief{Notes: task}, worktree, branch, "", rules, charter, level, osSandbox, snap, repo, bar)
}

// WorkerTaskBodyBrief is WorkerTaskBodyOS for a structured brief.
func WorkerTaskBodyBrief(harness string, brief WorkerBrief, worktree, branch, rules, charter, level, osSandbox string, snap settings.Snapshot, repo string) string {
	return WorkerTaskBodyBriefBar(harness, brief, worktree, branch, "", rules, charter, level, osSandbox, snap, repo, nil)
}

// WorkerTaskBodyBriefBar renders a structured brief with the project's bar in
// the header and, when base is another worker's branch, the line that the
// worker's own worktree already starts at its head.
func WorkerTaskBodyBriefBar(harness string, brief WorkerBrief, worktree, branch, base, rules, charter, level, osSandbox string, snap settings.Snapshot, repo string, bar []BarRule) string {
	b := workerPreambleBar(harness, snap, repo, bar)
	if harness == "agy" {
		b += "\n- " + snap.String("delivery.worker_agy_commit_instruction", settings.Where{})
	}
	b += "\n- " + WorkerPermissionsLine(level, harness, osSandbox)
	switch WorkerPermissionsOrDefault(level) {
	case WorkersReadOnly:
		b += ": you cannot write files; report findings only"
	case WorkersNoNetwork:
		b += ": no network: git fetch, package installs and web calls will fail"
	default:
		if osSandbox != "" {
			b += ": writes outside your worktree and /tmp fail (read-only file system)"
		}
	}
	if worktree != "" {
		b += "\n\nYou work in a git worktree of the project at " + worktree + " on the branch " + branch +
			", not in the user's own checkout."
		if harness != "agy" {
			b += " " + snap.String("delivery.worker_branch_prep", settings.Where{})
		}
		if note := WorkerBaseNote(base, worktree, snap); note != "" {
			b += "\n\n" + note
		}
	}
	if rules != "" {
		b += "\n\nProject rules for workers\n\n" + rules
	}
	if charter != "" {
		b += "\n\n" + charter
	}
	return b + "\n\nTask:\n" + brief.Text()
}

// WorkerTaskSummaryMax is the longest task line a status view gets.
const WorkerTaskSummaryMax = 120

// WorkerTaskSummary is the first line of the task in a worker's message: the
// text after the brief's "Task:" line, or the message itself, cut to
// WorkerTaskSummaryMax characters.
func WorkerTaskSummary(body string) string {
	if i := strings.Index(body, "\n\nTask:\n"); i >= 0 {
		body = body[i+len("\n\nTask:\n"):]
	}
	line := strings.TrimSpace(workerBriefBody(body))
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = strings.TrimSpace(line[:j])
	}
	runes := []rune(line)
	if len(runes) > WorkerTaskSummaryMax {
		line = strings.TrimSpace(string(runes[:WorkerTaskSummaryMax-3])) + "..."
	}
	return line
}

// workerBriefBody drops a leading section heading from a structured brief, so
// a one-line summary shows the problem and not the word "Problem:".
func workerBriefBody(body string) string {
	body = strings.TrimSpace(body)
	for _, heading := range []string{"Problem:", "Evidence:", "Done:", "Hypothesis (yours to drop if wrong):", "Scope:", "Notes:"} {
		if strings.HasPrefix(body, heading) {
			if rest := strings.TrimSpace(strings.TrimPrefix(body, heading)); rest != "" {
				return rest
			}
		}
	}
	return body
}

// WorkerTitleMax is the longest worker title a caller may give.
const WorkerTitleMax = 60

// ValidWorkerTitle checks a title the caller gives: one line, at most
// WorkerTitleMax characters. An empty title is allowed and means the title is
// derived from the task.
func ValidWorkerTitle(title string) error {
	if strings.ContainsAny(title, "\r\n") {
		return fmt.Errorf("the title must be one line, with no newlines")
	}
	if n := len([]rune(strings.TrimSpace(title))); n > WorkerTitleMax {
		return fmt.Errorf("the title is %d characters: the limit is %d", n, WorkerTitleMax)
	}
	return nil
}

// workerBriefSetup is the start of a setup sentence in a worker brief. Such a
// sentence says where the work is, not what it is, so a title skips it.
var workerBriefSetup = []string{
	"repo:", "base:", "branch:", "worktree:", "project:", "setup:", "environment:",
	"read agents.md", "read claude.md", "read docs/", "read the docs", "read readme",
	"problem:", "evidence:", "done:", "hypothesis", "scope:", "notes:",
}

func workerBriefBoilerplate(sentence string) bool {
	s := strings.ToLower(strings.TrimSpace(sentence))
	for _, prefix := range workerBriefSetup {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// workerTitleSentence is the first sentence of the brief that says what the
// task is. Setup sentences such as "Repo: ..." and "Read AGENTS.md first." are
// skipped.
func workerTitleSentence(task string) string {
	for _, line := range strings.Split(task, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		start := 0
		for i := 0; i < len(line); i++ {
			if line[i] != '.' && line[i] != '!' && line[i] != '?' {
				continue
			}
			if i+1 < len(line) && line[i+1] != ' ' && line[i+1] != '\t' {
				continue
			}
			if sentence := strings.TrimSpace(line[start : i+1]); sentence != "" && !workerBriefBoilerplate(sentence) {
				return sentence
			}
			start = i + 1
		}
		if sentence := strings.TrimSpace(line[start:]); sentence != "" && !workerBriefBoilerplate(sentence) {
			return sentence
		}
	}
	return ""
}

// clipWorkerTitle cuts a title to WorkerTitleMax characters at a word
// boundary, with an ellipsis when it had to cut.
func clipWorkerTitle(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= WorkerTitleMax {
		return text
	}
	cut := WorkerTitleMax - 1
	for cut > 0 && !unicode.IsSpace(runes[cut]) {
		cut--
	}
	if cut == 0 {
		return string(runes[:WorkerTitleMax-1]) + "…"
	}
	return strings.TrimSpace(string(runes[:cut])) + "…"
}

// WorkerTitle is the title shown for a worker. An explicit title wins; without
// one it is the first sentence of the brief that is not setup, cut to
// WorkerTitleMax characters. When the brief has nothing else, it is fallback,
// the worker's name.
func WorkerTitle(title, task, fallback string) string {
	if t := strings.TrimSpace(title); t != "" {
		return clipWorkerTitle(t)
	}
	if sentence := workerTitleSentence(task); sentence != "" {
		return clipWorkerTitle(sentence)
	}
	return strings.TrimSpace(fallback)
}

// Isolation modes of a worker. Empty means the default: a worktree when the
// project is a repository, the project folder when it is not (W17).
// IsolationNone opts out, for a read-only or non-code task.
const (
	IsolationWorktree = "worktree"
	IsolationNone     = "none"
)

// ValidIsolation tells whether s is an isolation a caller may ask for.
func ValidIsolation(s string) bool { return s == "" || s == IsolationWorktree || s == IsolationNone }

// WorktreeBranch is the branch of a worker's worktree.
func WorktreeBranch(workerID string) string {
	return WorktreeBranchWithSettings(workerID, settings.Snapshot{})
}
func WorktreeBranchWithSettings(workerID string, snap settings.Snapshot) string {
	return snap.String("delivery.worker_branch_prefix", settings.Where{}) + workerID
}

// Hand-back statuses a worker may report, and the statuses the daemon adds
// when a worker ends without one.
const (
	StatusDone     = "done"
	StatusBlocked  = "blocked"
	StatusFailed   = "failed"
	StatusTimedOut = "timed out"
	StatusStopped  = "stopped"
)

// ValidHandbackStatus tells whether a worker may report s.
func ValidHandbackStatus(s string) bool {
	return s == StatusDone || s == StatusBlocked || s == StatusFailed
}

// WorkerNotice holds the facts of the completion notice (W11). The daemon
// measures them; the worker does not write them.
type WorkerNotice struct {
	VisualEvidence
	Evidence    *WorkerEvidence
	Permissions string
	Status      string
	Session     string
	Harness     string
	Model       string
	Project     string
	Host        string
	// Title is the worker's short title. Empty for an older record.
	Title   string
	RunTime time.Duration
	Tokens  string // "1200 in, 340 out", empty when the harness does not say
	// Context is the context at the end of the run, as ContextText writes it
	// ("210k / 1M"), empty when the harness does not say.
	Context string
	Cost    string // "0.0123", empty when unknown
	// APICost is the run's cost at API prices, as "$0.43 at API prices", empty
	// when no price list is known (decision 0160).
	APICost  string
	Worktree string
	Branch   string
	DiffStat string // git diff --stat of the worktree, empty when none
	Note     string // why it ended, for a worker that did not hand back
	// Saved is true when the daemon committed the worker's unfinished changes
	// on its branch.
	Saved bool
	// WIPDetail describes a daemon WIP commit or a failure to make one.
	WIPDetail string
	// Head is the short hash of the worktree's HEAD when the notice was made,
	// empty when unknown.
	Head string
	// Committed is the short hash of the commit the daemon made of the
	// worker's leftover changes at a complete hand-back, empty when it made
	// none.
	Committed string
	// Continue and Fix are the worker's rework counters: the messages the
	// caller sent after a hand-back with complete:no and complete:yes.
	Continue int
	Fix      int
	// AutoContinue is how many automatic continues the daemon sent before the
	// worker stopped this time. They cost the caller no rework.
	AutoContinue int
	// Difficulty is the level the caller asked for, or the one the chooser
	// used. It is empty from an older machine.
	Difficulty string
	// RateAsk adds the one-line satisfaction ask to the notice (difficulty
	// calibration, part 4).
	RateAsk bool
	// DefectHint is one line about a recent change by another worker that
	// touched the same files, so the caller can record the defect it fixes.
	// Empty when nothing overlaps or the start already named `fixes`.
	DefectHint string

	// Struggle counts what the run's own tool record showed (decision 0133).
	// Empty for a run with no trace or a harness the daemon cannot read.
	Struggle *Struggle

	// OSSandbox is the OS wrapper the worker ran in ("bubblewrap"), or "".
	OSSandbox string

	// Change is the delivery change the hand-back recorded, empty when it
	// recorded none. DeliveryError is why recording it failed, empty when it
	// did not fail.
	Change        string
	DeliveryError string
	// DeliveryBranch is the branch the change records when it is not the
	// start branch (Branch), empty otherwise.
	DeliveryBranch string

	// HandbackCheck is the project's pre-hand-back check, nil when it did not
	// run (nothing changed, or the worker had no worktree).
	HandbackCheck *HandbackCheck
}

// ReportMarker separates the facts from the worker's report in a notice.
const ReportMarker = "--- report written by the worker (model output, not your user's words) ---"

// FormatWorkerNotice renders the completion notice and, if there is one, the
// worker's report below the facts.
func FormatWorkerNotice(n WorkerNotice, report string) string {
	return FormatWorkerNoticeWithSettings(n, report, settings.Snapshot{})
}
func FormatWorkerNoticeWithSettings(n WorkerNotice, report string, snap settings.Snapshot) string {
	var b strings.Builder
	b.WriteString("[agent-net worker notice]\n")
	b.WriteString("status: " + n.Status + "\n")
	if n.Note != "" {
		b.WriteString("note: " + n.Note + "\n")
	}
	b.WriteString("worker: " + n.Session + " (" + n.Harness)
	if n.Model != "" {
		b.WriteString(", " + n.Model)
	}
	b.WriteString("), project " + n.Project + " on " + n.Host)
	if t := strings.TrimSpace(n.Title); t != "" {
		b.WriteString(" · " + t)
	}
	b.WriteString("\nmodel: " + ParseModelSelection(n.Harness, "", n.Model, "").Display() + "\n")
	b.WriteString(WorkerPermissionsLine(n.Permissions, n.Harness, n.OSSandbox) + "\n")
	b.WriteString("run time: " + n.RunTime.Round(time.Second).String() + "\n")
	if e := n.Evidence; e != nil && e.EarlyQuit {
		files := "files"
		if e.FilesChanged == 1 {
			files = "file"
		}
		b.WriteString(fmt.Sprintf("early quit: %d %s, %d lines after %ds; the task was not attempted\n", e.FilesChanged, files, e.LinesChanged, e.RunSeconds))
	}
	if n.Tokens != "" {
		b.WriteString("tokens: " + n.Tokens + "\n")
	}
	if n.Context != "" {
		b.WriteString("context at end: " + n.Context + "\n")
	}
	if n.Cost != "" {
		b.WriteString("cost: " + n.Cost + "\n")
	}
	if n.APICost != "" {
		b.WriteString("cost: " + n.APICost + "\n")
	}
	if n.Continue > 0 || n.Fix > 0 {
		// A first run has no rework; the line appears once there is one.
		b.WriteString(fmt.Sprintf("continue: %d, fix: %d\n", n.Continue, n.Fix))
	}
	if n.AutoContinue > 0 {
		b.WriteString(fmt.Sprintf("auto-continues: %d\n", n.AutoContinue))
	}
	if line := StruggleLine(n.Struggle); line != "" {
		b.WriteString(line + "\n")
	}
	if n.Worktree != "" {
		b.WriteString("worktree: " + n.Worktree + " (branch " + n.Branch + ")\n")
		if n.Head != "" {
			b.WriteString("head: " + n.Head + "\n")
		}
		if n.Committed != "" {
			b.WriteString("the daemon committed your leftover changes as " + n.Committed + "\n")
		}
		if n.Change != "" {
			b.WriteString("change: " + n.Change + "\n")
			if n.DeliveryBranch != "" {
				b.WriteString("recorded branch: " + n.DeliveryBranch + "\n")
			}
		}
		if n.DeliveryError != "" {
			b.WriteString("delivery record: " + n.DeliveryError + "\n")
		}
		if n.HandbackCheck != nil {
			b.WriteString(n.HandbackCheck.NoticeLine() + "\n")
			if n.HandbackCheck.Status == HandbackCheckFailed || n.HandbackCheck.Status == HandbackCheckTimedOut {
				if tail := strings.TrimRight(n.HandbackCheck.OutputTail, "\n"); tail != "" {
					// The command runs project code; its output must not pass
					// for agent-net's own markers.
					b.WriteString("hand-back check output:\n" + NeutralizeMarkers(tail) + "\n")
				}
			}
		}
		if n.Saved {
			b.WriteString("unfinished changes were saved as a commit on branch " + n.Branch + "\n")
		}
		if n.WIPDetail != "" {
			b.WriteString(n.WIPDetail + "\n")
		}
		if n.DiffStat == "" {
			b.WriteString("changes: none\n")
		} else {
			b.WriteString("changes (git diff --stat):\n" + n.DiffStat + "\n")
		}
	}
	b.WriteString(n.VisualEvidence.Lines())
	if n.DefectHint != "" {
		b.WriteString(n.DefectHint + "\n")
	}
	if n.RateAsk {
		b.WriteString(FormatRateAsk(n.Session, n.Model, n.Difficulty, n.RunTime) + "\n")
	}
	if strings.TrimSpace(report) != "" {
		b.WriteString("\n" + ReportMarker + "\n" + strings.TrimSpace(report) + "\n")
	}
	b.WriteString("\n" + noticeChecklistWithSettings(n.Worktree, strings.TrimSpace(report) != "", snap, acceptReportLine(n, snap)))
	if snap.DeliveryPipelineCutover && n.Project != "" {
		if p, err := PipelineFromSettings(snap, n.Project); err == nil {
			if merge, ok := p.Find(StepRef{Step: StepMerge}); ok && merge.Owner.Kind != OwnerCaller {
				b.WriteString("\nThe merge step is assigned to " + NeutralizeMarkers(merge.Owner.String()) + ".")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ChecklistMarker starts the checklist that agent-net adds at the end of every
// notice (W17). It is agent-net's own text, not the worker's.
const ChecklistMarker = "[agent-net checklist]"

func noticeChecklist(worktree string, learned bool) string {
	return noticeChecklistWithSettings(worktree, learned, settings.Snapshot{})
}

// acceptReportLine tells a caller who owns the accept step what to do. Callers
// read the checklist as "check the work" and never reported the step, so the
// line names the change and gives the exact call. It is empty when another
// node owns accept, or when no change was recorded.
func acceptReportLine(n WorkerNotice, snap settings.Snapshot) string {
	if !snap.DeliveryPipelineCutover || n.Project == "" || n.Change == "" {
		return ""
	}
	p, err := PipelineFromSettings(snap, n.Project)
	if err != nil {
		return ""
	}
	if accept, ok := p.Find(StepRef{Step: StepAccept}); !ok || accept.Owner.Kind != OwnerCaller {
		return ""
	}
	return "You own the accept step of " + n.Change + ". Report it: agent_step_report change=" + n.Change +
		" step=accept result=pass commit=<head>, or result=close with a one-line reason if nothing will be merged."
}

// noticeChecklistWithSettings writes the checklist. extra lines go before the
// learning line, which stays last.
func noticeChecklistWithSettings(worktree string, learned bool, snap settings.Snapshot, extra ...string) string {
	where := "the changes in the project folder"
	if worktree != "" {
		where = "the diff in " + worktree
	}
	line := ChecklistMarker + " " + strings.ReplaceAll(snap.String("delivery.caller_review_and_merge", settings.Where{}), "{{where}}", NeutralizeMarkers(where))
	line += "\nTest scope: delivery.contract.test (the test step owns suites and CI)."
	line += "\nFound a defect in earlier work? Set fixes=<change> on the fix, or file defect_found_later. Rework from your own brief: brief_defect."
	for _, e := range extra {
		if e != "" {
			line += "\n" + e
		}
	}
	if learned {
		line += "\nIf you learned something that would change how to pick or brief this model next time, record it with agent_learn."
	}
	return line
}

// ModelAuthFailure recognizes provider sign-in failures, separately from billing
// and rate limits. Numeric codes must be complete tokens.
func ModelAuthFailure(detail string) bool {
	detail = strings.ToLower(detail)
	if strings.Contains(detail, "not logged in") {
		return true
	}
	for _, token := range strings.FieldsFunc(detail, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if token == "401" || token == "403" {
			return true
		}
	}
	return false
}

// WorkerRunState announces a new run before its harness registers, or rolls
// back that same run when starting it fails.
type WorkerRunState struct {
	Session       string `json:"session"`
	RunGeneration int    `json:"run_generation"`
	Ended         bool   `json:"ended,omitempty"`
}

// FrameRecentWorkerChanges is the daemon to gateway read a worker hand-back
// makes: the merged changes of a project that another worker made recently.
const FrameRecentWorkerChanges = "worker.recent_changes"

// RecentWorkerChangesRequest asks for the merged changes of one project in a
// window. ExcludeSession leaves out the worker that is handing back itself,
// and ExcludeChange leaves out the change it is working on. Limit is 1 to 5.
type RecentWorkerChangesRequest struct {
	Project        string `json:"project"`
	ExcludeSession string `json:"exclude_session,omitempty"`
	ExcludeChange  string `json:"exclude_change,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

// RecentWorkerChange is one merged change a hand-back may be fixing.
type RecentWorkerChange struct {
	ChangeID      string `json:"change_id"`
	Title         string `json:"title,omitempty"`
	WorkerID      string `json:"worker_id,omitempty"`
	WorkerSession string `json:"worker_session,omitempty"`
	MergedAt      int64  `json:"merged_at"`
	MergedCommit  string `json:"merged_commit,omitempty"`
}

// RecentWorkerChangesResult answers FrameRecentWorkerChanges, newest first.
type RecentWorkerChangesResult struct {
	Changes []RecentWorkerChange `json:"changes"`
}
