package protocol

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
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
	// Data. These only read.
	OpInfo     = "info"           // answers OperatorInfo
	OpNodes    = "nodes"          // answers NodesInfo
	OpAgents   = "list"           // takes QueryAgents, answers AgentsResult
	OpProjects = "projects"       // takes QueryProjects, answers ProjectsResult
	OpEvents   = "events"         // takes QueryEvents, answers EventsResult
	OpMessage  = "message_status" // takes QueryMessage, answers MessageResult
	OpWorkers  = "workers"        // takes nothing, answers WorkersResult

	// Owner-only data (decision 0008): CPU and memory, the text of a session,
	// the session counts. They share one gate with OpSend (decision 0008): the
	// owner's command line or TUI, never an agent session.
	OpUsage        = "usage"         // takes nothing, answers UsageResult
	OpUsageTop     = "usage_top"     // takes QueryUsageTop, answers UsageTop
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
	OpSessionPolicy = "session_policy" // takes SessionPolicyRequest, answers SessionPolicyResult

	// OpNodeRename gives a machine a display name that every machine sees
	// (decision 0015). Owner only, no agent tool.
	OpNodeRename = "node_rename" // takes NodeRenameRequest, answers NodeRenameResult

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
)

// Graph operator methods are available only to the owner.
const (
	OpHeldList       = "held_list"
	OpHeldAnswer     = "held_answer"
	OpGraphGet       = "graph_get"
	OpCharterSet     = "charter_set"
	OpCharterHistory = "charter_history"
	OpNodeAdd        = "node_add"
	OpNodeRemove     = "node_remove"
	OpEdgeSet        = "edge_set"
	OpEdgeRemove     = "edge_remove"
	OpEdgeHistory    = "edge_history"
	OpGraphUndo      = "graph_undo"
	// OpNodeSettingsGet and OpNodeSettingsSet read and change a node's
	// overrides of its maturity defaults (decision 0022). Owner only, no
	// agent tool.
	OpNodeSettingsGet = "node_settings_get"
	OpNodeSettingsSet = "node_settings_set"
)

// OperatorMethods lists every operator method, for tests and docs.
var OperatorMethods = []string{OpInfo, OpNodes, OpAgents, OpProjects, OpEvents, OpMessage, OpWorkers,
	OpUsage, OpUsageTop, OpGraphView, OpSessionText, OpCounts, OpSessionEnter, OpSessionTrust, OpSessionPolicy, OpNodeRename,
	OpHeldList, OpHeldAnswer, OpGraphGet, OpCharterSet, OpCharterHistory, OpNodeAdd, OpNodeRemove, OpEdgeSet, OpEdgeRemove, OpEdgeHistory, OpGraphUndo,
	OpNodeSettingsGet, OpNodeSettingsSet,
	OpIntegrationsGet, OpIntegrationsSet, OpHiddenGet, OpHiddenSet, OpPlaybookGet, OpPlaybookSet, OpSend, OpWorkerStart, OpWorkerStop, OpWorkerProfiles, OpWorkerProfileAdd, OpWorkerProfileRemove}

// HiddenItems is the owner's shared TUI visibility preference. Session IDs
// remain hidden across machines; node names identify graph items on Map.
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
}

// AgentsResult answers OpAgents.
type AgentsResult struct {
	Agents []AgentView `json:"agents"`
}

// ProjectsResult answers OpProjects.
type ProjectsResult struct {
	Projects []ProjectView `json:"projects"`
}

// EventsResult answers OpEvents.
type EventsResult struct {
	Events []FeedEvent `json:"events"`
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
}

// WorkerStartRequest is OpWorkerStart.
type WorkerStartRequest struct {
	// Project is the name of a registered or seen project. Empty means the
	// project of the calling session.
	Project string `json:"project"`
	// Machine picks the machine (host name or node ID) when the project is on
	// more than one. Optional.
	Machine string `json:"machine,omitempty"`
	// Harness is the agent harness to run: "opencode" (the others come later).
	Harness string `json:"harness"`
	// Model is "provider/model", or a loose name such as "deepseek 4.1" that
	// the gateway resolves against the machine's model list. Optional: empty
	// means the harness default.
	Model string `json:"model,omitempty"`
	// Difficulty selects the minimum score for automatic model choice.
	Difficulty string `json:"difficulty,omitempty"`
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
	// ModelNote says how a loose model name was resolved ("deepseek 4.1 ->
	// opencode-go/deepseek-v4.1-flash"). Empty when the name was used as given.
	ModelNote string `json:"model_note,omitempty"`
	// Worktree and Branch are set when the worker got its own worktree.
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	// Permissions is the level the worker runs with (sandboxed, no-network
	// or read-only), set by the owner per project (decision 0020).
	Permissions string `json:"permissions,omitempty"`
	// Task is the message that carried the task, with its message ID.
	Task *Message `json:"task,omitempty"`
	// Queued is true when the task message waits in the outbox.
	Queued bool `json:"queued,omitempty"`
	// Status is "started" or "queued". A queued start has no SessionID yet:
	// the machine has no room now, QueueID names the start (agent_stop takes
	// it), Position is its place in that machine's queue, and QueueReason
	// says why it waits. A worker notice follows when it starts.
	Status      string `json:"status,omitempty"`
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
}

// IsComplete tells whether the hand-back reports the whole task done.
func (r WorkerHandbackRequest) IsComplete() bool { return r.Complete == nil || *r.Complete }

// WorkerHandbackResult answers OpWorkerHandback.
type WorkerHandbackResult struct {
	Status   string   `json:"status"`
	Complete bool     `json:"complete"`
	Sent     *Message `json:"sent,omitempty"`
	Queued   bool     `json:"queued,omitempty"`
}

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
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	Hostname  string `json:"hostname"`
	Harness   string `json:"harness"`
	Model     string `json:"model,omitempty"`
	Project   string `json:"project"`
	Path      string `json:"path"`
	Online    bool   `json:"online"`
	Status    string `json:"status"`
	// Owner is the session that started the worker; OwnerName names it.
	Owner     string `json:"owner"`
	OwnerName string `json:"owner_name,omitempty"`
	StartedAt int64  `json:"started_at"`
	// Task is the first line of the task the owner gave, cut to about 120
	// characters. Empty when this machine did not start the worker.
	Task string `json:"task,omitempty"`
	// ContextTokens, ContextLimit and Tokens are the worker's usage, as in
	// WorkerInfo. 0 when the harness cannot say.
	ContextTokens int64 `json:"context_tokens,omitempty"`
	ContextLimit  int64 `json:"context_limit,omitempty"`
	Tokens        int64 `json:"tokens,omitempty"`
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
			Harness: a.Harness, Model: a.Model, Project: a.Worker.Project, Path: a.Cwd, Online: a.Online, Status: a.Status,
			Owner: a.Worker.Owner, OwnerName: names[a.Worker.Owner], StartedAt: a.Worker.StartedAt,
			Task: a.Worker.Task, ContextTokens: a.Worker.ContextTokens, ContextLimit: a.Worker.ContextLimit, Tokens: a.Worker.Tokens})
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

// HeldAnswer is an owner decision for a request awaiting approval.
type HeldAnswer struct {
	MessageID string `json:"message_id"`
	Action    string `json:"action"`
}
