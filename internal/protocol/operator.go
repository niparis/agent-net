package protocol

import "sort"

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
	OpSessionText  = "session_text"  // takes TextQuery, answers TextPage
	OpCounts       = "counts"        // takes nothing, answers SessionCounts
	OpSessionEnter = "session_enter" // takes EnterRequest, answers EnterPlan

	// Actions. Only the owner at the keyboard may use OpSend (see OwnerName);
	// OpWorkerStart and OpWorkerStop are also open to agent sessions, with
	// the limits of decision 0002.
	OpSend        = "owner_send"   // takes OwnerSendRequest, answers MessageSent
	OpWorkerStart = "worker_start" // takes WorkerStartRequest, answers WorkerStartResult
	OpWorkerStop  = "worker_stop"  // takes WorkerStopRequest, answers WorkerStopResult

	// OpWorkerHandback is for a worker's own session: its final report. It is
	// not an operator action and not open to the command line.
	OpWorkerHandback = "worker_handback" // takes WorkerHandbackRequest, answers WorkerHandbackResult
)

// OperatorMethods lists every operator method, for tests and docs.
var OperatorMethods = []string{OpInfo, OpNodes, OpAgents, OpProjects, OpEvents, OpMessage, OpWorkers,
	OpUsage, OpUsageTop, OpSessionText, OpCounts, OpSessionEnter, OpSend, OpWorkerStart, OpWorkerStop}

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
	// Task is what the worker should do. It is sent as a message from the caller.
	Task string `json:"task"`
	// Isolation is "" (the default: a new worktree on the branch
	// agent-net/<worker-id> when the project is a repository), "worktree"
	// (the same, and an error when it is not a repository) or "none" (work in
	// the project folder).
	Isolation string `json:"isolation,omitempty"`
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
	// Task is the message that carried the task, with its message ID.
	Task *Message `json:"task,omitempty"`
	// Queued is true when the task message waits in the outbox.
	Queued bool `json:"queued,omitempty"`
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
}

// WorkerHandbackResult answers OpWorkerHandback.
type WorkerHandbackResult struct {
	Status string   `json:"status"`
	Sent   *Message `json:"sent,omitempty"`
	Queued bool     `json:"queued,omitempty"`
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
}

// WorkersResult answers OpWorkers.
type WorkersResult struct {
	Workers []WorkerView `json:"workers"`
}

// WorkersOf picks the workers out of a list of sessions, with the name of
// each owner when the list has the owner's session. Online workers come
// first, then the newest.
func WorkersOf(agents []AgentView) []WorkerView {
	names := map[string]string{}
	for _, a := range agents {
		names[a.SessionID] = a.Profile.Name
	}
	out := []WorkerView{}
	for _, a := range agents {
		if a.Worker == nil {
			continue
		}
		out = append(out, WorkerView{SessionID: a.SessionID, NodeID: a.NodeID, Hostname: a.Hostname,
			Harness: a.Harness, Model: a.Model, Project: a.Worker.Project, Path: a.Cwd, Online: a.Online, Status: a.Status,
			Owner: a.Worker.Owner, OwnerName: names[a.Worker.Owner], StartedAt: a.Worker.StartedAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].StartedAt > out[j].StartedAt
	})
	return out
}
