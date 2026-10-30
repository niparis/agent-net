// Package operator is the one interface a person uses to look at the agent
// network and act on it (decision 0008). It has two parts:
//
//   - the methods and their request and answer types, in internal/protocol
//     (operator.go), which are plain JSON and know nothing about a front end;
//   - this package, a Go client of those methods over the local daemon's
//     socket, with a small composed Snapshot for screens that show everything
//     at once.
//
// The terminal screen uses only this package. The phone app will call the same
// methods through the gateway.
package operator

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Operator is everything a front end can ask and do. Reads never change
// anything. The actions are the ones decision 0008 lists.
type Operator interface {
	// Snapshot is the state of the network as the local daemon sees it.
	Snapshot(ctx context.Context) (Snapshot, error)
	// Events returns feed events after afterID, oldest first. With afterID 0
	// it returns the newest limit events.
	Events(ctx context.Context, afterID int64, limit int) ([]protocol.FeedEvent, error)
	// Message fetches one message with its body. The gateway only gives it to
	// the machines that have a session in the conversation.
	Message(ctx context.Context, id string) (protocol.Message, error)
	// SendAsOwner sends a message from the owner (the person at this machine)
	// to a session.
	SendAsOwner(ctx context.Context, to, body string) (protocol.MessageSent, error)
	// StartWorker starts a worker and sends it its task as the owner.
	StartWorker(ctx context.Context, req protocol.WorkerStartRequest) (protocol.WorkerStartResult, error)
	// StopWorker ends a worker.
	StopWorker(ctx context.Context, session string) error
	// Workers lists the workers on the network with their owners.
	Workers(ctx context.Context, q protocol.WorkersQuery) ([]protocol.WorkerView, error)
	// Usage is the CPU and memory of every machine and session. Owner only.
	Usage(ctx context.Context) (protocol.UsageResult, error)
	// UsageTop is the heaviest processes of one session. Owner only.
	UsageTop(ctx context.Context, session string) (protocol.UsageTop, error)
	// GraphView reads the component graph: nodes with charters, edges and the
	// newest charter changes. Owner only. It changes nothing.
	GraphView(ctx context.Context, q protocol.QueryGraph) (protocol.GraphView, error)
	// SessionText is a page of what happens inside a session. Owner only.
	SessionText(ctx context.Context, q protocol.TextQuery) (protocol.TextPage, error)
	// SessionEnter says how to take over the terminal for a session. Owner only.
	SessionEnter(ctx context.Context, q protocol.EnterRequest) (protocol.EnterPlan, error)
	// SetPeerTrust turns peer trust on or off for a session: whether it may act
	// on requests from other agents without asking (decision 0012). Owner only.
	SetPeerTrust(ctx context.Context, session string, on bool) error
	// SetSessionPolicy sets (or with DryRun only checks) the delegation policy
	// of a session or its project (decision 0014). Owner only.
	SetSessionPolicy(ctx context.Context, q protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error)
	// RenameNode gives a machine a display name that every machine sees, or
	// clears it with an empty name (decision 0015). Owner only.
	RenameNode(ctx context.Context, nodeID, name string) (protocol.NodeRenameResult, error)
	// Integrations reads the owner's setup answers for the whole network
	// (decision 0021). Owner only.
	Integrations(ctx context.Context) (protocol.IntegrationsResult, error)
	// SetIntegrations stores the setup answers for every machine. Owner only.
	SetIntegrations(ctx context.Context, in protocol.Integrations) (protocol.IntegrationsResult, error)
	// Counts is how many sessions are online and offline. Owner only.
	Counts(ctx context.Context) (protocol.SessionCounts, error)
	Close()
}

// Snapshot is the state of the network as one daemon sees it.
type Snapshot struct {
	// Connected tells whether the daemon has a link to the gateway.
	Connected bool
	NodeID    string // the machine the operator runs on
	Hostname  string // its host name, when the daemon says it
	Version   string // its agent-net version
	// OwnerSession is the owner's session ID on this machine ("" if unknown).
	OwnerSession string
	// Launch is false when this machine refuses workers.
	Launch             bool
	Scores             []protocol.ModelScore
	DifficultyMinimums map[string]float64
	Nodes              []protocol.NodeView
	// Agents are the sessions, with the owner pseudo-sessions.
	Outcomes []protocol.OutcomeInboxEntry
	Agents   []protocol.AgentView
	Held     []protocol.Message
	Edges    []protocol.Edge
	// Projects is the project list (registered, seen or both). Empty when the
	// gateway cannot list them; a screen then shows no project source.
	Projects []protocol.ProjectView
	// Playbook is what the network learned, for the owner's "Learned" list
	// (decision 0022).
	Playbook []protocol.PlaybookNote
}

// Workers lists the workers among the agents, with their owners.
func (s Snapshot) Workers() []protocol.WorkerView { return protocol.WorkersOf(s.Agents) }

// Problem says why the last call failed.
type Problem int

const (
	ProblemNone    Problem = iota
	ProblemDaemon          // the daemon cannot be reached
	ProblemGateway         // the daemon cannot reach the gateway
	ProblemOther           // the daemon refused a call
)

// ErrDaemonDown and ErrGatewayDown are the two failures a screen explains.
var (
	ErrDaemonDown  = errors.New("daemon not reachable")
	ErrGatewayDown = errors.New("gateway not reachable")
)

// ErrFeedUnavailable means the daemon or the gateway is older than the event
// feed.
var ErrFeedUnavailable = errors.New("event feed not available")

// ErrTooOld means the daemon or the gateway is older than the call.
var ErrTooOld = errors.New("too old for this action")

// ClassifyError maps an error from an Operator to a Problem.
func ClassifyError(err error) Problem {
	switch {
	case err == nil:
		return ProblemNone
	case errors.Is(err, ErrDaemonDown):
		return ProblemDaemon
	case errors.Is(err, ErrGatewayDown):
		return ProblemGateway
	}
	return ProblemOther
}

// IsOldPeer tells whether an error text comes from a daemon that does not know
// a method (it says "not available to cli clients" or "unknown method") or a
// gateway that does not know a frame.
func IsOldPeer(msg string) bool {
	for _, s := range []string{"unknown method", "unknown frame type", "not available to cli clients"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Timeout is how long one read may take. Starting a worker takes longer
// (StartTimeout).
const Timeout = 5 * time.Second

// StartTimeout is how long StartWorker may take: the machine starts a harness.
const StartTimeout = 2 * time.Minute
