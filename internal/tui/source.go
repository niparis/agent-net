// Package tui is the read-only terminal view of the agent network (`agent-net
// tui`). It gets all its data from the local daemon, so it works on any
// machine of the network. The model and update logic (model.go, feed.go) know
// nothing about the terminal drawing (view.go) or the daemon (source.go).
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// Snapshot is the state of the network as one daemon sees it.
type Snapshot struct {
	// Connected tells whether the daemon has a link to the gateway.
	Connected bool
	NodeID    string // the machine the TUI runs on
	Version   string // its agent-net version
	Nodes     []protocol.NodeView
	Agents    []protocol.AgentView
	// Projects is the project list (registered, seen or both). Empty when the
	// gateway cannot list them; the screen then shows no project source.
	Projects []protocol.ProjectView
}

// Problem says why the last refresh failed.
type Problem int

const (
	ProblemNone    Problem = iota
	ProblemDaemon          // the daemon cannot be reached
	ProblemGateway         // the daemon cannot reach the gateway
	ProblemOther           // the daemon refused a call
)

// ErrDaemonDown and ErrGatewayDown are the two failures the screen explains.
var (
	ErrDaemonDown  = errors.New("daemon not reachable")
	ErrGatewayDown = errors.New("gateway not reachable")
)

// ClassifyError maps an error from a Source to a Problem.
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

// ErrFeedUnavailable means the daemon or the gateway is older than the event
// feed. The screen then shows the sessions without the message feed.
var ErrFeedUnavailable = errors.New("event feed not available")

// Source is where the TUI reads from. It only reads.
type Source interface {
	Snapshot(ctx context.Context) (Snapshot, error)
	// Events returns feed events after afterID, oldest first. With afterID 0
	// it returns the newest limit events.
	Events(ctx context.Context, afterID int64, limit int) ([]protocol.FeedEvent, error)
	// Message fetches one message with its body. The gateway only gives it to
	// the machines that have a session in the conversation.
	Message(ctx context.Context, id string) (protocol.Message, error)
	Close()
}

// DaemonSource reads from the local daemon over its Unix socket, with the
// read-only calls of a CLI client. It connects again by itself after the
// daemon restarts.
type DaemonSource struct {
	path string

	mu sync.Mutex
	cl *client.Client
}

// NewDaemonSource returns a source for the daemon socket at path. It does not
// connect until the first call, so a daemon that starts later is found.
func NewDaemonSource(path string) *DaemonSource { return &DaemonSource{path: path} }

func (s *DaemonSource) conn(ctx context.Context) (*client.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cl != nil && !s.cl.Dead() {
		return s.cl, nil
	}
	if s.cl != nil {
		s.cl.Close()
		s.cl = nil
	}
	cl, err := client.Dial(s.path)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", ErrDaemonDown, s.path)
	}
	if _, err := cl.Call(ctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err != nil {
		cl.Close()
		if client.IsConnError(err) {
			return nil, fmt.Errorf("%w (%s)", ErrDaemonDown, s.path)
		}
		return nil, err
	}
	s.cl = cl
	return cl, nil
}

func (s *DaemonSource) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	cl, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := cl.Call(ctx, method, params)
	if client.IsConnError(err) {
		s.mu.Lock()
		if s.cl == cl {
			cl.Close()
			s.cl = nil
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("%w (%s)", ErrDaemonDown, s.path)
	}
	if err != nil && strings.Contains(err.Error(), "not connected to the gateway") {
		return nil, fmt.Errorf("%w: %v", ErrGatewayDown, err)
	}
	return raw, err
}

func (s *DaemonSource) Snapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	raw, err := s.call(ctx, "info", nil)
	if err != nil {
		return snap, err
	}
	var info struct {
		Connected bool   `json:"connected"`
		NodeID    string `json:"node_id"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return snap, err
	}
	snap.Connected, snap.NodeID, snap.Version = info.Connected, info.NodeID, info.Version
	if !info.Connected {
		return snap, nil
	}
	// The machine list is a nicety: without it the machines come from the agents.
	if raw, err := s.call(ctx, "nodes", nil); err == nil {
		var ni protocol.NodesInfo
		if json.Unmarshal(raw, &ni) == nil {
			snap.Nodes = ni.Nodes
		}
	} else if errors.Is(err, ErrDaemonDown) || errors.Is(err, ErrGatewayDown) {
		return snap, err
	}
	raw, err = s.call(ctx, "list", protocol.QueryAgents{})
	if err != nil {
		return snap, err
	}
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return snap, err
	}
	snap.Agents = r.Agents
	// Best effort, like the node list.
	if raw, err := s.call(ctx, "projects", protocol.QueryProjects{}); err == nil {
		var pr struct {
			Projects []protocol.ProjectView `json:"projects"`
		}
		if json.Unmarshal(raw, &pr) == nil {
			snap.Projects = pr.Projects
		}
	} else if errors.Is(err, ErrDaemonDown) || errors.Is(err, ErrGatewayDown) {
		return snap, err
	}
	return snap, nil
}

func (s *DaemonSource) Events(ctx context.Context, afterID int64, limit int) ([]protocol.FeedEvent, error) {
	raw, err := s.call(ctx, "events", protocol.QueryEvents{AfterID: afterID, Limit: limit})
	if err != nil {
		var re client.RemoteError
		if errors.As(err, &re) && (isOldPeer(err.Error())) {
			return nil, fmt.Errorf("%w: %v", ErrFeedUnavailable, err)
		}
		return nil, err
	}
	var r struct {
		Events []protocol.FeedEvent `json:"events"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Events, nil
}

// isOldPeer tells whether an error text comes from a daemon that does not know
// the events method (it says "not available to cli clients" or "unknown
// method") or a gateway that does not know query.events.
func isOldPeer(msg string) bool {
	for _, s := range []string{"unknown method", "unknown frame type", "not available to cli clients"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (s *DaemonSource) Message(ctx context.Context, id string) (protocol.Message, error) {
	raw, err := s.call(ctx, "message_status", protocol.QueryMessage{MessageID: id})
	if err != nil {
		return protocol.Message{}, err
	}
	var r struct {
		Message protocol.Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return protocol.Message{}, err
	}
	return r.Message, nil
}

func (s *DaemonSource) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cl != nil {
		s.cl.Close()
		s.cl = nil
	}
}

// Timeout is how long one refresh may take.
const Timeout = 5 * time.Second
