// Package tui is the live terminal view of the agent network (`agent-net
// tui`), with a few actions: send a message as the owner, start a worker and
// stop a worker. It gets all its data and does all its actions through the
// operator interface (internal/operator, decision 0008), which is the same one
// the phone app will use. The model and update logic (model.go, feed.go,
// forms.go) know nothing about the terminal drawing (view.go) or the daemon.
package tui

import (
	"errors"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The TUI's names for the operator's types. They are aliases, so the TUI has
// no types of its own for data that the phone shares.
type (
	// Snapshot is the state of the network as one daemon sees it.
	Snapshot = operator.Snapshot
	// Problem says why the last refresh failed.
	Problem = operator.Problem
	// Source is where the TUI reads from and acts through: the operator.
	Source = operator.Operator
	// DaemonSource is the operator over the local daemon's socket.
	DaemonSource = operator.Client
)

// Usage is the answer of the operator's usage method.
type Usage = protocol.UsageResult

// ErrUsageUnavailable means the daemon or gateway is older than the CPU and
// memory data. The fake sources of the tests return it too.
var ErrUsageUnavailable = operator.ErrTooOld

// isUnavailable tells whether an error from the owner-only data calls means
// the daemon or the gateway cannot answer them (it is older than decision 0007).
func isUnavailable(err error) bool {
	return errors.Is(err, ErrUsageUnavailable) || err != nil && operator.IsOldPeer(err.Error())
}

const (
	ProblemNone    = operator.ProblemNone
	ProblemDaemon  = operator.ProblemDaemon
	ProblemGateway = operator.ProblemGateway
	ProblemOther   = operator.ProblemOther
)

var (
	// ErrDaemonDown and ErrGatewayDown are the two failures the screen explains.
	ErrDaemonDown  = operator.ErrDaemonDown
	ErrGatewayDown = operator.ErrGatewayDown
	// ErrFeedUnavailable means the daemon or the gateway is older than the
	// event feed. The screen then shows the sessions without the message feed.
	ErrFeedUnavailable = operator.ErrFeedUnavailable
	// ClassifyError maps an error from a Source to a Problem.
	ClassifyError = operator.ClassifyError
	isOldPeer     = operator.IsOldPeer
)

// NewDaemonSource returns the operator for the daemon socket at path.
func NewDaemonSource(path string) *DaemonSource { return operator.New(path) }

// Timeout is how long one refresh may take.
const Timeout = operator.Timeout
