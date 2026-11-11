package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The operator interface: the one set of methods a person uses to look at the
// network and act on it. The terminal screen (`agent-net tui`) calls these
// through the local daemon. The phone app will call the same methods through
// the gateway (decision 0008), so every request and answer is plain JSON with
// no shape that belongs to one front end.
//
// A method name is the "method" of a local socket request, and would be the
// path of an HTTP route. Requests and answers are the types below.

// Operator method names.
const (
	OpProposalList     = "proposal_list"
	OpProposalAnswer   = "proposal_answer"
	OpProposalStartAck = "proposal_start_ack"
	OpAppsRename       = "apps.rename"
	OpAppsGrant        = "apps.grant"
	OpAppsUngrant      = "apps.ungrant"
	// Data. These only read.
	OpInfo          = "info"          // answers OperatorInfo
	OpNodes         = "nodes"         // answers NodesInfo
	OpAgents        = "list"          // takes QueryAgents, answers AgentsResult
	OpProjects      = "projects"      // takes QueryProjects, answers ProjectsResult
	OpEvents        = "events"        // takes QueryEvents, answers EventsResult
	OpConversations = "conversations" // takes QueryConversations, answers ConversationPage
	// OpConversationMessages reads every stored message of one conversation
	// for the owner. It is owner only: agents keep the node-scoped messages
	// method.
	OpConversationMessages = "conversation_messages" // takes QueryConversationMessages, answers ConversationMessagesResult
	OpMessage              = "message_status"        // takes QueryMessage, answers MessageResult
	OpWorkers              = "workers"               // takes nothing, answers WorkersResult
	OpToolsDifferences     = "tools_differences"     // owner-only summary of skill and MCP server differences

	// Owner-only data (decision 0008): CPU and memory, the text of a session,
	// the session counts. They share one gate with OpSend (decision 0008): the
	// owner's command line or TUI, never an agent session.
	OpUsage        = "usage"         // takes nothing, answers UsageResult
	OpUsageTop     = "usage_top"     // takes QueryUsageTop, answers UsageTop
	OpGraphTalk    = "graph_talk"    // answers project message counts
	OpGraphView    = "graph_view"    // takes QueryGraph, answers GraphView
	OpSessionText  = "session_text"  // takes TextQuery, answers TextPage
	OpCounts       = "counts"        // takes nothing, answers SessionCounts
	OpSessionEnter = "session_enter" // takes EnterRequest, answers EnterPlan
	// OpSessionTrust turns peer trust on or off for a session (decision 0012).
	// It changes what a session may do on a peer's request, so it is owner
	// only and no agent tool calls it.
	OpSessionTrust = "session_trust" // takes SessionTrustRequest, answers SessionTrustResult
	// OpSessionPolicy sets the delegation policy of a session or its project
	// (decision 0014). Owner only, no agent tool.
	OpSessionPolicy    = "session_policy" // takes SessionPolicyRequest, answers SessionPolicyResult
	OpSessionAgentSet  = "session_agent_set"
	OpSessionAgentList = "session_agent_list"

	// OpSessionRename gives a session a name the owner chose, or with an empty
	// name returns it to the automatic project name (decision 0033). Owner
	// only: no agent tool sets it.
	OpSessionRename = "session_rename" // takes SessionRenameRequest, answers SessionRenameResult

	// Project display names are set and read by the owner.
	OpProjectDisplayGet = "project_display_get"
	OpProjectDisplaySet = "project_display_set"
	// OpNodeRename gives a machine a display name that every machine sees
	// (decision 0015). Owner only, no agent tool.
	OpNodeRename = "node_rename" // takes NodeRenameRequest, answers NodeRenameResult

	// Machine actions require owner authority and a node_id.
	OpMachineRemove = "machine_remove"
	OpMachineRotate = "machine_rotate"

	// OpIntegrationsGet and OpIntegrationsSet read and write the owner's setup
	// integrations for the whole network (decision 0021). Owner only, no agent
	// tool: the answers decide what runs with the owner's consent.
	OpIntegrationsGet = "integrations_get" // answers IntegrationsResult
	OpIntegrationsSet = "integrations_set" // takes Integrations, answers IntegrationsResult
	// OpHiddenGet and OpHiddenSet read and write the owner's hidden TUI items
	// for the whole network. Owner only, no agent tool.
	OpHiddenGet = "hidden_get" // answers HiddenItems
	OpHiddenSet = "hidden_set" // takes and answers HiddenItems

	// Actions. Only the owner at the keyboard may use OpSend (see OwnerName);
	// OpWorkerStart and OpWorkerStop are also open to agent sessions, with
	// the limits of decision 0002.
	OpSend        = "owner_send"   // takes OwnerSendRequest, answers MessageSent
	OpWorkerStart = "worker_start" // takes WorkerStartRequest, answers WorkerStartResult
	OpWorkerStop  = "worker_stop"  // takes WorkerStopRequest, answers WorkerStopResult

	// OpWorkerHandback is for a worker's own session: its final report. It is
	// not an operator action and not open to the command line.
	OpWorkerHandback = "worker_handback" // takes WorkerHandbackRequest, answers WorkerHandbackResult

	// OpPlaybookGet and OpPlaybookSet read and write the owner's notes about
	// models (decision 0022). Owner only: the note goes into every start
	// result for that model.
	OpPlaybookGet = "playbook_get" // takes PlaybookGetRequest, answers PlaybookGetResult
	OpPlaybookSet = "playbook_set" // takes PlaybookSetRequest, answers PlaybookGetResult

	// OpLearn records one finding from an agent session (decision 0022).
	// Findings are proposals: the gateway promotes a pattern to a playbook
	// note. Open to agent sessions, not to the command line.
	OpLearn = "agent_learn" // takes LearnRequest, answers LearnResult

	// OpCharterDraft proposes one charter value drafted from AGENTS.md
	// (decision 0088). Open to agent sessions, not to the command line.
	OpCharterDraft = "agent_charter_draft" // takes CharterDraftRequest, answers CharterDraftResult

	// OpPermissionPropose proposes exact Claude Code permission allow rules
	// for registered repositories (decision 0127). Open to every session,
	// including the CTO, and refused for the command line. It changes no file:
	// the owner answers an owner-only ask, and only then does the gateway tell
	// each target machine to apply.
	OpPermissionPropose = "agent_permission_propose" // takes PermissionProposeRequest, answers PermissionProposeResult
	// OpPermissionUndo removes exactly the rules one approval added. Owner only.
	OpPermissionUndo = "permission_undo" // takes PermissionUndoRequest, answers PermissionUndoResult

	// OpRate records one caller's rating of a finished worker (difficulty
	// calibration, part 4). Open to agent sessions, not to the command line.
	OpRate = "agent_rate" // the daemon method behind agent_worker_rate
	// OpWorkerDefect reports a defect found later on a worker's task as the
	// owner. It is owner only, so the owner's score path stays separate from
	// the caller's agent_worker_rate.
	OpWorkerDefect = "worker_defect" // takes DefectRequest, answers RateResult
)

// Graph operator methods are available only to the owner.
const (
	OpHeldList         = "held_list"
	OpAsksList         = "asks_list"
	OpDeskLog          = "desk_log"
	OpDeskSettle       = "desk_settle"
	OpHeldAnswer       = "held_answer"
	OpGraphGet         = "graph_get"
	OpTeamWrite        = "team_write"
	OpLeadSet          = "lead_set"
	OpTeamHistory      = "team_history"
	OpCharterSet       = "charter_set"
	OpCharterHistory   = "charter_history"
	OpNodeAdd          = "node_add"
	OpNodeRemove       = "node_remove"
	OpEdgeSet          = "edge_set"
	OpEdgeRemove       = "edge_remove"
	OpEdgeHistory      = "edge_history"
	OpDeliveryOwnerSet = "delivery_owner_set"
	OpGraphUndo        = "graph_undo"
	OpRepoCreate       = "repo_create"
	OpRepoArchive      = "repo_archive"
	OpAgentCreate      = "agent_create"
	OpAgentTombstone   = "agent_tombstone"
	// OpNodeSettingsGet and OpNodeSettingsSet read and change a node's
	// overrides of its maturity defaults (decision 0022). Owner only, no
	// agent tool.
	OpNodeSettingsGet = "node_settings_get"
	OpNodeSettingsSet = "node_settings_set"
)

// OperatorMethods lists every operator method, for tests and docs.
var OperatorMethods = []string{OpWorkerFloorsReset, OpOperatorUndo, OpMergerGet, OpMergerRestore, OpAppsPublicRestore, OpWorkerLog, OpProposalList, OpProposalAnswer, OpProposalStartAck, OpInfo, OpNodes, OpAgents, OpProjects, OpEvents, OpConversations, OpConversationMessages, OpMessage, OpWorkers, OpToolsDifferences,
	OpUsage, OpUsageTop, OpGraphView, OpGraphTalk, OpSessionText, OpCounts, OpSessionEnter, OpSessionTrust, OpFolderTrust, OpFolderMCP, OpProcessStop, OpStandingOrders, OpSessionPolicy, OpSessionAgentSet, OpSessionAgentList, OpSessionRename, OpNodeRename, OpMachineRemove, OpMachineRotate, OpProjectDisplayGet, OpProjectDisplaySet,
	OpHeldList, OpAsksList, OpChiefGet, OpChiefSet, OpAskHistory, OpChiefSorts, OpAskUndo, OpAskMark, OpDeskLog, OpDeskSettle, OpHeldAnswer, OpLeadSet, OpTeamWrite, OpTeamHistory, OpGraphGet, OpCharterSet, OpCharterHistory, OpNodeAdd, OpNodeRemove, OpEdgeSet, OpEdgeRemove, OpEdgeHistory, OpDeliveryOwnerSet, OpMergerSet, OpGraphUndo, OpRepoCreate, OpRepoArchive, OpAgentCreate, OpAgentTombstone,
	OpNodeSettingsGet, OpNodeSettingsSet, OpQualityGet, OpQualitySet, OpTimeZoneGet, OpTimeZoneSet,
	OpIntegrationsGet, OpIntegrationsSet, OpHiddenGet, OpHiddenSet, "onboarding_get", "onboarding_set", "onboarding_restart", OpPlaybookGet, OpPlaybookSet, OpSend, OpWorkerStart, OpWorkerStop, OpWorkerProfiles, OpWorkerProfileAdd, OpWorkerProfileRemove, OpWorkerDefect, OpDifficultyJudgePick, OpJudgmentAdd, OpPermissionUndo}

// HiddenItems is the owner's shared TUI visibility preference. Session IDs
// remain hidden across machines; node names identify graph items on Graph.
type HiddenItems struct {
	Sessions []string `json:"sessions,omitempty"`
	Nodes    []string `json:"nodes,omitempty"`
}

// UsageResult answers OpUsage: this machine's own sample, and what the gateway
// has for every machine.
type UsageResult struct {
	NodeID       string       `json:"node_id"`
	Hostname     string       `json:"hostname"`
	Local        *UsageReport `json:"local"`
	Gateway      *UsageAnswer `json:"gateway"`
	GatewayError string       `json:"gateway_error"`
}

// OperatorInfo answers OpInfo: the state of the daemon the operator talks to.
type OperatorInfo struct {
	Connected  bool          `json:"connected"`
	NodeID     string        `json:"node_id"`
	Hostname   string        `json:"hostname,omitempty"`
	GatewayURL string        `json:"gateway_url"`
	Version    string        `json:"version"`
	Protocol   int           `json:"protocol"`
	Sessions   []SessionInfo `json:"sessions"`
	// OwnerSession is the session ID of the owner on this machine, "" until
	// the gateway has registered it.
	OwnerSession string `json:"owner_session,omitempty"`
	// Launch is false when this machine refuses workers (setup --no-launch).
	Launch bool `json:"launch"`
	// ProposalCount is how many CTO proposals wait for the owner on this
	// machine. It lets the screen notice a new proposal without listing every
	// proposal on every refresh. It is 0 from an older daemon.
	ProposalCount int `json:"proposal_count,omitempty"`
}

// AgentsResult answers OpAgents.
type AgentsResult struct {
	Agents []AgentView `json:"agents"`
	// NotModified means the caller's IfVersion already matches the agents
	// topic, so Agents holds no rows and the caller keeps its copy.
	NotModified bool `json:"not_modified,omitempty"`
}

// ProjectsResult answers OpProjects.
type ProjectsResult struct {
	Projects []ProjectView `json:"projects"`
}

// EventsResult answers OpEvents.
type EventsResult struct {
	Events []FeedEvent `json:"events"`
}

// QueryConversations asks for stored conversations, newest activity first.
// Cursor is the Next value of the previous page; an empty cursor starts at the
// newest conversation. Limit is capped by the gateway.
type QueryConversations struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ConversationView is one stored conversation as the owner's Messages tab
// shows it: the two sides, how many messages it holds, and its latest line.
type ConversationView struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	FromName string `json:"from_name,omitempty"`
	ToName   string `json:"to_name,omitempty"`
	Count    int    `json:"message_count"`
	// Type is the newest message's type (request, reply, notification).
	Type string `json:"type,omitempty"`
	// LastLine is the first line of the newest message, cleaned for one
	// terminal line and cut.
	LastLine string `json:"last_line,omitempty"`
	// State is the newest message's state.
	State         string    `json:"state,omitempty"`
	LastMessageID string    `json:"last_message_id,omitempty"`
	At            time.Time `json:"at"`
	FromCTO       bool      `json:"from_cto,omitempty"`
	// OwnerInstruction tells whether the newest message carries the owner's
	// prompt excerpt. It shows the same you→ marker as the live feed.
	OwnerInstruction bool `json:"owner_instruction,omitempty"`
	// WorkerSession groups every conversation that involves a worker session
	// into one row, with the ID "worker:<session>". It survives the session
	// record, because the gateway reads the worker's notice and task messages.
	WorkerSession string `json:"worker_session,omitempty"`
	// WorkerOwner is the session that started the worker, from the task
	// message or its stored worker record.
	WorkerOwner string `json:"worker_owner,omitempty"`
	// Task is the first line of the task the caller sent the worker, cleaned
	// and cut for a list row.
	Task string `json:"task,omitempty"`
	// FromHarness and ToHarness are the two sessions' harness names, so the
	// Messages tab can show a sender's type (session, worker, app, system)
	// without another read. Empty for a session whose record is gone.
	FromHarness string `json:"from_harness,omitempty"`
	ToHarness   string `json:"to_harness,omitempty"`
	// OwnerNamed is true when any message in the conversation was sent by the
	// owner session, carries the owner's approval, or cites the owner's words.
	OwnerNamed bool `json:"owner_named,omitempty"`
	// Blocked is true when any message reports a blocked action.
	Blocked bool `json:"blocked,omitempty"`
	// System is true when agent-net sent a message in this conversation.
	// SystemLine is that message's body, so the screen can classify the
	// notice by its first marker even when the agent answered last. SystemTo
	// is that message's recipient, which is the agent the notice was for.
	System     bool   `json:"system,omitempty"`
	SystemLine string `json:"system_line,omitempty"`
	SystemTo   string `json:"system_to,omitempty"`
	// PeakHour and MedianHour are the busiest hour's message count and the
	// median hourly count over the conversation's life. The screen marks a
	// burst when PeakHour is at least 20 and more than 3 times MedianHour.
	PeakHour   int `json:"peak_hour,omitempty"`
	MedianHour int `json:"median_hour,omitempty"`
}

// ConversationPage answers OpConversations. Total counts every stored
// conversation on the gateway, not only this page. Next is empty on the last
// page.
type ConversationPage struct {
	Conversations []ConversationView `json:"conversations"`
	Total         int                `json:"total"`
	Next          string             `json:"next,omitempty"`
}

// QueryConversationMessages asks for every stored message of one conversation.
// It is owner only and covers the whole network, not only this machine's
// sessions.
type QueryConversationMessages struct {
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit,omitempty"`
}

// ConversationMessagesResult answers OpConversationMessages, newest first.
type ConversationMessagesResult struct {
	Messages []Message `json:"messages"`
}

// MessageResult answers OpMessage.
type MessageResult struct {
	Message Message `json:"message"`
}

// MessageSent answers a send. Queued is true when the gateway was not
// reachable: the daemon keeps the message and sends it on reconnect.
type MessageSent struct {
	Message        *Message `json:"message,omitempty"`
	Queued         bool     `json:"queued,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

// The owner as a sender. Each daemon registers one pseudo-session for the
// person at its machine: harness OwnerHarness, native ID OwnerNativeID, shown
// as OwnerName(host). Only the daemon creates it. A message from it comes from
// the owner in person, not from an agent.
const (
	OwnerHarness  = "owner"
	OwnerNativeID = "owner"
)

// OwnerName is how the owner of a machine is shown: "you (on <host>)".
func OwnerName(host string) string { return "you (on " + host + ")" }

// IsOwner tells whether a harness name is the owner pseudo-session.
func IsOwner(harness string) bool { return harness == OwnerHarness }

// OwnerSendRequest is OpSend: a message from the owner to a session.
type OwnerSendRequest struct {
	// To is a session ID, or an exact name that is unique among online agents.
	To   string `json:"to"`
	Body string `json:"body"`
	// IdempotencyKey is optional. A client that may retry sets one, so the
	// gateway stores the message once.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// ConversationID is optional. When set, the message is a reply in that
	// conversation instead of opening a new one. The conversation must exist
	// and To must be one of its sides, or the gateway refuses the send.
	ConversationID string `json:"conversation_id,omitempty"`
}

// WorkerStartRequest is OpWorkerStart.
type WorkerStartRequest struct {
	// Project is the name of a registered or seen project. Empty means the
	// project of the calling session.
	Project string `json:"project"`
	// Machine picks the machine (host name or node ID) when the project is on
	// more than one. Optional.
	Machine string `json:"machine,omitempty"`
	// Harness is the agent harness to run; empty allows automatic selection.
	Harness string `json:"harness"`
	// Model is a model ID, loose name or profile. Legacy provider prefixes and
	// effort suffixes are accepted; new callers use Provider and Effort.
	Model     string   `json:"model,omitempty"`
	NotFamily []string `json:"not_family,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Effort    string   `json:"effort,omitempty"`
	// Difficulty selects the minimum score for automatic model choice.
	Difficulty string   `json:"difficulty,omitempty"`
	Touches    []string `json:"touches,omitempty"`
	// Title is one short line naming the worker, at most 60 characters and
	// with no newlines. Empty means the gateway derives it from Task.
	Title string `json:"title,omitempty"`
	// Fixes names a shipped change the new worker fixes. agent-net records a
	// defect on that change's worker, unless it has one for that change.
	// FixesSeverity is 1 to 4 (default 2).
	Fixes         string `json:"fixes,omitempty"`
	FixesSeverity int    `json:"fixes_severity,omitempty"`
	// Task is what the worker should do. It is sent as a message from the caller.
	Task string `json:"task"`
	// Isolation is "" (the default: a new worktree on the branch
	// agent-net/<worker-id> when the project is a repository), "worktree"
	// (the same, and an error when it is not a repository) or "none" (work in
	// the project folder).
	Isolation string `json:"isolation,omitempty"`
	// Base is an existing local branch to start the worker worktree from.
	Base string `json:"base,omitempty"`
	// Continue is the session ID or worker ID of an ended worker that the
	// caller started. The new worker works on from that worker's branch, with
	// its first task. Project, harness and model default to the old worker's.
	// Task is then optional and is added as "Also: ...".
	Continue string `json:"continue,omitempty"`
	// AutoContinue lets the daemon send the worker its own continues when it
	// stops early (a partial hand-back, or idle without a hand-back), up to
	// DefaultAutoContinues times, before the caller is told. Absent means on.
	AutoContinue *bool `json:"auto_continue,omitempty"`
	// MCPServers is "approve" or "disable". For a Claude worker it is recorded
	// for the .mcp.json servers in the worker's folder before Claude starts, on
	// top of the choice copied from the project's main checkout. Empty records
	// nothing. Not allowed with Continue.
	MCPServers string `json:"mcp_servers,omitempty"`
}

// WorkerStartResult answers OpWorkerStart.
type WorkerStartResult struct {
	// SessionID is the worker's session ID. The worker's replies arrive as
	// messages in the caller's session (or in the owner feed).
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	Hostname  string `json:"hostname"`
	Project   string `json:"project"`
	Path      string `json:"path"`
	Harness   string `json:"harness"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Effort    string `json:"effort,omitempty"`
	// ModelNote says how a loose model name was resolved ("deepseek 4.1 ->
	// opencode-go/deepseek-v4.1-flash"). Empty when the name was used as given.
	ModelNote string `json:"model_note,omitempty"`
	// OwnerLine is one line for the caller to pass on to its user: the model
	// and the judge's difficulty. Empty for a continued worker.
	OwnerLine string `json:"owner_line,omitempty"`
	// Reminder is a short note for the caller when it named a model itself
	// instead of leaving the choice to the picker. Empty otherwise.
	Reminder string `json:"reminder,omitempty"`
	// Hint is one line about a defect this start may fix: the recent changes
	// in the project that `fixes` can name. Empty when the task does not sound
	// like a fix or when `fixes` was set.
	Hint string `json:"hint,omitempty"`
	// Worktree and Branch are set when the worker got its own worktree.
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	// Permissions is the level the worker runs with (sandboxed, no-network
	// or read-only), set by the owner per project (decision 0020).
	Permissions string `json:"permissions,omitempty"`
	// OSSandbox is the OS wrapper the worker runs in ("bubblewrap"), or ""
	// (docs/design/worker-os-sandbox.md).
	OSSandbox string `json:"os_sandbox,omitempty"`
	// Task is the message that carried the task, with its message ID.
	Task *Message `json:"task,omitempty"`
	// Title is the worker's short title: the one that was asked for, or the
	// one derived from the task.
	Title string `json:"title,omitempty"`
	// Queued is true when the task message waits in the outbox.
	Queued bool `json:"queued,omitempty"`
	// Status is "starting", "started" or "queued". A starting worker has a
	// session ID and receives its task after checkout and launch. A queued
	// start has no SessionID yet:
	// the machine has no room now, QueueID names the start (agent_worker_stop takes
	// it), Position is its place in that machine's queue, and QueueReason
	// says why it waits. A worker notice follows when it starts.
	//
	// Status "accepted" means the daemon took the start but the judge or the
	// launch is still running: StartID names it, and a worker notice follows
	// when the worker starts, queues or fails. SessionID is empty.
	Status  string `json:"status,omitempty"`
	StartID string `json:"start_id,omitempty"`
	// Note is one line about the answer, set for an accepted start.
	Note        string `json:"note,omitempty"`
	QueueID     string `json:"queue_id,omitempty"`
	Position    int    `json:"position,omitempty"`
	QueueReason string `json:"queue_reason,omitempty"`
	// MachineNote says why this machine was picked when the project is on
	// several. Empty otherwise.
	MachineNote string `json:"machine_note,omitempty"`
	// Track is the gateway's record for the chosen harness and model, with
	// Runs 0 when nothing has run there yet (decision 0022).
	Track *TrackRecord `json:"track,omitempty"`
	// Playbooks are the fresh notes for the chosen model and harness, the
	// project and the machine, if any (decision 0022).
	Playbooks []PlaybookNote `json:"playbooks,omitempty"`
}

// WorkerStopRequest is OpWorkerStop.
type WorkerStopRequest struct {
	// Session is the worker's session ID.
	Session string `json:"session"`
}

// WorkerStopResult answers OpWorkerStop.
type WorkerStopResult struct {
	Session string `json:"session"`
	Stopped bool   `json:"stopped"`
}

// WorkerHandbackRequest is OpWorkerHandback.
type WorkerHandbackRequest struct {
	// Report is the final report. It is the message.
	Report string `json:"report"`
	// Status is done (default), blocked or failed.
	Status string `json:"status,omitempty"`
	// Complete is false when the worker hands back partial work. It is absent
	// in a request from an older worker or tool and then means true.
	Complete *bool `json:"complete,omitempty"`
	// Files are absolute paths in the worker's own worktree.
	Files []string `json:"files,omitempty"`
}

// IsComplete tells whether the hand-back reports the whole task done.
func (r WorkerHandbackRequest) IsComplete() bool { return r.Complete == nil || *r.Complete }

// WorkerHandbackResult answers OpWorkerHandback.
type WorkerHandbackResult struct {
	Status   string   `json:"status"`
	Complete bool     `json:"complete"`
	Sent     *Message `json:"sent,omitempty"`
	Queued   bool     `json:"queued,omitempty"`
	// AutoContinued is true when the daemon kept the worker running with its
	// own continue instead of telling the caller.
	AutoContinued bool `json:"auto_continued,omitempty"`
}

// RecentWorkerWindow is how long a worker that ended stays visible: the
// gateway keeps its row for an owner's panel query, and the daemon keeps its
// own record for the recent list. One constant, so both sides agree.
const RecentWorkerWindow = 2 * time.Minute

// WorkersQuery is the optional request of OpWorkers. An empty query lists the
// running workers, as OpWorkers always did.
type WorkersQuery struct {
	// OwnerClaude keeps only the workers owned by the local Claude Code
	// session with this native ID (the transcript file's name).
	OwnerClaude string `json:"owner_claude,omitempty"`
	// Recent also lists workers that ended in the last two minutes.
	Recent bool `json:"recent,omitempty"`
}

// WorkerView is one worker as the operator sees it.
type WorkerView struct {
	Assessment *WorkerAssessment `json:"assessment,omitempty"`
	SessionID  string            `json:"session_id"`
	NodeID     string            `json:"node_id"`
	Hostname   string            `json:"hostname"`
	Harness    string            `json:"harness"`
	Model      string            `json:"model,omitempty"`
	Project    string            `json:"project"`
	Path       string            `json:"path"`
	// Name is the session name the gateway gave the worker, when it knows it.
	Name   string `json:"name,omitempty"`
	Online bool   `json:"online"`
	Status string `json:"status"`
	// Owner is the session that started the worker; OwnerName names it.
	Owner     string `json:"owner"`
	OwnerName string `json:"owner_name,omitempty"`
	StartedAt int64  `json:"started_at"`
	// Task is the first line of the task the owner gave, cut to about 120
	// characters. Empty when this machine did not start the worker.
	Task string `json:"task,omitempty"`
	// Title is the worker's short title: the one the caller gave, or a line
	// derived from Task. Empty only for an older worker record.
	Title string `json:"title,omitempty"`
	// ContextTokens, ContextLimit and Tokens are the worker's usage, as in
	// WorkerInfo. 0 when the harness cannot say.
	ContextTokens int64 `json:"context_tokens,omitempty"`
	ContextLimit  int64 `json:"context_limit,omitempty"`
	Tokens        int64 `json:"tokens,omitempty"`
	// Continue and Fix are the worker's rework counters, and the difficulty
	// asked and used are the levels recorded at its start, as in WorkerInfo.
	Continue        int    `json:"continue,omitempty"`
	Fix             int    `json:"fix,omitempty"`
	DifficultyAsked string `json:"difficulty_asked,omitempty"`
	DifficultyUsed  string `json:"difficulty_used,omitempty"`
	// EndedAt, EndStatus and EndNote are set for a worker that ended in the
	// last two minutes, when the query asked for recent workers.
	EndedAt   int64  `json:"ended_at,omitempty"`
	EndStatus string `json:"end_status,omitempty"`
	EndNote   string `json:"end_note,omitempty"`
}

// WorkersResult answers OpWorkers.
type WorkersResult struct {
	Workers []WorkerView `json:"workers"`
	// Queued lists the starts that wait for room, on every machine.
	Queued []QueuedWorker `json:"queued,omitempty"`
}

// CompactTokens writes a token count short enough for a band: 840, 2k, 84k,
// 345k, 1M, 1.5M. It returns "" for zero.
func CompactTokens(n int64) string {
	if n <= 0 {
		return ""
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	s := fmt.Sprintf("%.1f", float64(n)/1000000)
	s = strings.TrimSuffix(s, ".0")
	return s + "M"
}

// ContextText writes the context a worker's model saw on its last step and the
// model's context window: "84k / 1M (8%)". With no limit it is just the
// tokens; with no tokens it is empty.
func ContextText(tokens, limit int64) string {
	if tokens <= 0 {
		return ""
	}
	text := CompactTokens(tokens)
	if limit <= 0 {
		return text
	}
	percent := int64(math.Round(float64(tokens) / float64(limit) * 100))
	return fmt.Sprintf("%s / %s (%d%%)", text, CompactTokens(limit), percent)
}

// WorkersOf picks the workers out of a list of sessions, with the name of
// each owner when the list has the owner's session. A worker that ended is not
// listed: its record stays for a follow-up that starts it again, but it is
// offline (W7). The newest comes first.
func WorkersOf(agents []AgentView) []WorkerView {
	names := map[string]string{}
	for _, a := range agents {
		names[a.SessionID] = a.Profile.Name
	}
	out := []WorkerView{}
	for _, a := range agents {
		if a.Worker == nil || !a.Online {
			continue
		}
		out = append(out, WorkerView{SessionID: a.SessionID, NodeID: a.NodeID, Hostname: a.Hostname,
			Harness: a.Harness, Model: a.Model, Project: a.Worker.Project, Path: a.Cwd, Name: names[a.SessionID],
			Online: a.Online, Status: a.Status,
			Owner: a.Worker.Owner, OwnerName: names[a.Worker.Owner], StartedAt: a.Worker.StartedAt,
			Task: a.Worker.Task, Title: a.Worker.Title,
			ContextTokens: a.Worker.ContextTokens, ContextLimit: a.Worker.ContextLimit, Tokens: a.Worker.Tokens,
			Assessment: a.Worker.Assessment, Continue: a.Worker.Continue, Fix: a.Worker.Fix, DifficultyAsked: a.Worker.DifficultyAsked, DifficultyUsed: a.Worker.DifficultyUsed})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})
	return out
}

// SessionTrustRequest is OpSessionTrust.
type SessionTrustRequest struct {
	SessionID string `json:"session_id"`
	On        bool   `json:"on"`
}

// SessionTrustResult answers OpSessionTrust.
type SessionTrustResult struct {
	SessionID string `json:"session_id"`
	On        bool   `json:"on"`
}

// SessionRenameRequest is OpSessionRename. An empty Name returns the session
// to its automatic project name.
type SessionRenameRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

// SessionRenameResult answers OpSessionRename with the name now listed.
type SessionRenameResult struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

// NodeRenameRequest is OpNodeRename. An empty Name clears the display name.
type NodeRenameRequest struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

// NodeRenameResult answers OpNodeRename.
type NodeRenameResult struct {
	NodeID string `json:"node_id"`
	// Hostname is the name now listed for the machine.
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name,omitempty"`
	Reported    string `json:"reported_hostname,omitempty"`
}

// MaxNodeName is the longest display name of a machine.
const MaxNodeName = 63

// CleanNodeName checks a display name and returns it trimmed. It is 1 to 63
// letters, digits, '-', '_' or '.', and starts with a letter or digit, because
// it can be the key of enter_ssh_hosts. An empty name is fine and means "clear".
func CleanNodeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > MaxNodeName {
		return "", fmt.Errorf("a machine name is at most %d characters", MaxNodeName)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if alnum {
			continue
		}
		if i == 0 {
			return "", errors.New("a machine name starts with a letter or digit")
		}
		if c != '-' && c != '_' && c != '.' {
			return "", errors.New("a machine name has only letters, digits, '-', '_' and '.'")
		}
	}
	return s, nil
}

// CTOProposal is an owner-visible stored proposal and its outcome.
type CTOProposal struct {
	Messages  []CTOProposalMessage `json:"messages,omitempty"`
	ID        string               `json:"id"`
	NodeID    string               `json:"node_id"`
	Tool      string               `json:"tool"`
	Action    string               `json:"action"`
	Args      json.RawMessage      `json:"args"`
	Reason    string               `json:"reason"`
	CreatedAt time.Time            `json:"created_at"`
	Status    string               `json:"status"`
	Note      string               `json:"note,omitempty"`
	Result    json.RawMessage      `json:"result,omitempty"`
}

type CTOProposalMessage struct {
	Sender string    `json:"sender"`
	At     time.Time `json:"at"`
}

type CTOProposalAnswer struct {
	NodeID      string           `json:"node_id"`
	ID          string           `json:"proposal_id"`
	Action      string           `json:"action"`
	Confirm     []CTOProposalRef `json:"confirm,omitempty"`
	HerdrSocket string           `json:"herdr_socket,omitempty"`
	HerdrPaneID string           `json:"herdr_pane_id,omitempty"`
}

type CTOProposalRef struct {
	NodeID string `json:"node_id"`
	ID     string `json:"proposal_id"`
}

// HeldAnswer is an owner decision for a request awaiting approval.
type HeldAnswer struct {
	MessageID string `json:"message_id"`
	Action    string `json:"action"`
}

// ProjectDisplayRequest identifies a project and an optional display name.
// An empty name clears it.
type ProjectDisplayRequest struct {
	Project string `json:"project"`
	Name    string `json:"name"`
}
type ProjectDisplayResult struct {
	Project string `json:"project"`
	Name    string `json:"name"`
}

// Onboarding records the owner's shared getting-started progress.
type Onboarding struct {
	Steps    map[string]string `json:"steps"`
	Disabled bool              `json:"disabled"`
}
type OnboardingUpdate struct {
	Step     string `json:"step,omitempty"`
	State    string `json:"state,omitempty"`
	Disabled *bool  `json:"disabled,omitempty"`
}

// AppsPublicStatus describes the gateway's public connected-apps listener.
type AppsPublicStatus struct {
	On    bool   `json:"on"`
	URL   string `json:"url"`
	Port  int    `json:"port"`
	Error string `json:"error"`
	// Node is the Tailscale name of agent-net's own node when the Funnel
	// runs there (decision 0058). Empty for the machine's node or a tunnel.
	Node string `json:"node,omitempty"`
}

// CTOStartAck reports the outcome of a terminal handoff.
type CTOStartAck struct {
	ID      string `json:"proposal_id"`
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
}

// SessionAgentOption is one active agent the owner can assign.
type SessionAgentOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type SessionAgentSetRequest struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
	Owner     string `json:"owner_session,omitempty"`
}

type SessionAgentOptions struct {
	Agents []SessionAgentOption `json:"agents"`
}
