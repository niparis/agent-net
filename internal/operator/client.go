package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// Client is an Operator that talks to the local daemon over its Unix socket,
// as a CLI client. It connects again by itself after the daemon restarts.
type Client struct {
	path string

	mu     sync.Mutex
	cl     *client.Client
	uiPush func(protocol.UIRequest)
}

var _ Operator = (*Client)(nil)

// New returns a client for the daemon socket at path. It does not connect
// until the first call, so a daemon that starts later is found.
func New(path string) *Client { return &Client{path: path} }

// SetUIPush registers the TUI callback before the first connection.
func (s *Client) SetUIPush(push func(protocol.UIRequest)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uiPush = push
}

func (s *Client) conn(ctx context.Context) (*client.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cl != nil && !s.cl.Dead() {
		return s.cl, nil
	}
	if s.cl != nil {
		s.cl.Close()
		s.cl = nil
	}
	push := s.uiPush
	cl, err := client.DialPushData(s.path, func(method string, raw json.RawMessage) {
		if method != "cto_ui" || push == nil {
			return
		}
		var q protocol.UIRequest
		if json.Unmarshal(raw, &q) == nil {
			push(q)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", ErrDaemonDown, s.path)
	}
	if _, err := cl.Call(ctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol, "tui": s.uiPush != nil}); err != nil {
		cl.Close()
		if client.IsConnError(err) {
			return nil, fmt.Errorf("%w (%s)", ErrDaemonDown, s.path)
		}
		return nil, err
	}
	s.cl = cl
	return cl, nil
}

// Call makes one daemon call and classifies the failure.
func (s *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
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

// action is Call for the actions that an older daemon does not have: its
// refusal becomes ErrTooOld with a clear text.
func (s *Client) action(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := s.Call(ctx, method, params)
	var re client.RemoteError
	if errors.As(err, &re) && IsOldPeer(err.Error()) {
		return nil, fmt.Errorf("%w: the daemon on this machine is older than this command; upgrade it with `agent-net upgrade` and restart it (%v)", ErrTooOld, err)
	}
	return raw, err
}

func (s *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	raw, err := s.Call(ctx, protocol.OpInfo, nil)
	if err != nil {
		return snap, err
	}
	var info protocol.OperatorInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return snap, err
	}
	snap.Connected, snap.NodeID, snap.Version = info.Connected, info.NodeID, info.Version
	snap.Hostname, snap.OwnerSession = info.Hostname, info.OwnerSession
	// An older daemon does not say; it accepts workers only if it is new enough
	// to have the field, so "false" there is the safe reading.
	snap.Launch = info.Launch
	if !info.Connected {
		return snap, nil
	}
	// The machine list is a nicety: without it the machines come from the agents.
	if raw, err := s.Call(ctx, protocol.OpNodes, nil); err == nil {
		var ni protocol.NodesInfo
		if json.Unmarshal(raw, &ni) == nil {
			snap.Nodes = ni.Nodes
		}
	} else if errors.Is(err, ErrDaemonDown) || errors.Is(err, ErrGatewayDown) {
		return snap, err
	}
	if raw, err := s.Call(ctx, protocol.OpWorkerProfiles, nil); err == nil {
		_ = json.Unmarshal(raw, &snap.WorkerProfiles)
	}
	if raw, err := s.Call(ctx, protocol.OpWorkerProvidersGet, nil); err == nil {
		var providers protocol.WorkerProviders
		if json.Unmarshal(raw, &providers) == nil {
			snap.WorkerProviders = providers.Providers
		}
	}
	if raw, err := s.Call(ctx, protocol.OpProtectedProvidersGet, nil); err == nil {
		_ = json.Unmarshal(raw, &snap.ProtectedProviders)
	}
	if raw, err := s.Call(ctx, protocol.OpScoresGet, nil); err == nil {
		_ = json.Unmarshal(raw, &snap.Scores)
	}
	if raw, err := s.Call(ctx, "difficulty_get", nil); err == nil {
		_ = json.Unmarshal(raw, &snap.DifficultyMinimums)
	}
	raw, err = s.Call(ctx, protocol.OpAgents, protocol.QueryAgents{IncludeOwners: true})
	if err != nil {
		return snap, err
	}
	var r protocol.AgentsResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return snap, err
	}
	snap.Agents = r.Agents
	if raw, err := s.Call(ctx, "outcome_inbox", nil); err == nil {
		var in struct {
			Entries []protocol.OutcomeInboxEntry `json:"entries"`
		}
		if json.Unmarshal(raw, &in) == nil {
			snap.Outcomes = in.Entries
		}
	}
	if raw, err := s.Call(ctx, "learned_list", nil); err == nil {
		_ = json.Unmarshal(raw, &snap.Learned)
	}
	if raw, err := s.Call(ctx, protocol.OpHeldList, nil); err == nil {
		var held struct {
			Messages []protocol.Message `json:"messages"`
		}
		if json.Unmarshal(raw, &held) == nil {
			snap.Held = held.Messages
		}
	}
	if raw, err := s.Call(ctx, protocol.OpGraphGet, nil); err == nil {
		var graph protocol.Graph
		if json.Unmarshal(raw, &graph) == nil {
			snap.Edges = graph.Edges
		}
	}
	// Best effort, like the node list.
	if raw, err := s.Call(ctx, protocol.OpProjects, protocol.QueryProjects{}); err == nil {
		var pr protocol.ProjectsResult
		if json.Unmarshal(raw, &pr) == nil {
			snap.Projects = pr.Projects
		}
	} else if errors.Is(err, ErrDaemonDown) || errors.Is(err, ErrGatewayDown) {
		return snap, err
	}
	return snap, nil
}

func (s *Client) Events(ctx context.Context, afterID int64, limit int) ([]protocol.FeedEvent, error) {
	raw, err := s.Call(ctx, protocol.OpEvents, protocol.QueryEvents{AfterID: afterID, Limit: limit})
	if err != nil {
		var re client.RemoteError
		if errors.As(err, &re) && IsOldPeer(err.Error()) {
			return nil, fmt.Errorf("%w: %v", ErrFeedUnavailable, err)
		}
		return nil, err
	}
	var r protocol.EventsResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return r.Events, nil
}

func (s *Client) Message(ctx context.Context, id string) (protocol.Message, error) {
	raw, err := s.Call(ctx, protocol.OpMessage, protocol.QueryMessage{MessageID: id})
	if err != nil {
		return protocol.Message{}, err
	}
	var r protocol.MessageResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return protocol.Message{}, err
	}
	return r.Message, nil
}

func (s *Client) SendAsOwner(ctx context.Context, to, body string) (protocol.MessageSent, error) {
	var out protocol.MessageSent
	raw, err := s.action(ctx, protocol.OpSend, protocol.OwnerSendRequest{To: to, Body: body, IdempotencyKey: protocol.NewID("k")})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *Client) StartWorker(ctx context.Context, req protocol.WorkerStartRequest) (protocol.WorkerStartResult, error) {
	var out protocol.WorkerStartResult
	raw, err := s.action(ctx, protocol.OpWorkerStart, req)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *Client) StopWorker(ctx context.Context, session string) error {
	_, err := s.action(ctx, protocol.OpWorkerStop, protocol.WorkerStopRequest{Session: session})
	return err
}

func (s *Client) Workers(ctx context.Context, q protocol.WorkersQuery) ([]protocol.WorkerView, error) {
	raw, err := s.action(ctx, protocol.OpWorkers, q)
	if err != nil {
		return nil, err
	}
	var out protocol.WorkersResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Workers, nil
}

// The owner-only data methods. A daemon from before them refuses with
// "not available to cli clients"; the gate refuses an agent with its own text.

func (s *Client) Usage(ctx context.Context) (protocol.UsageResult, error) {
	var out protocol.UsageResult
	raw, err := s.action(ctx, protocol.OpUsage, nil)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) UsageTop(ctx context.Context, session string) (protocol.UsageTop, error) {
	var out protocol.UsageTop
	raw, err := s.action(ctx, protocol.OpUsageTop, protocol.QueryUsageTop{SessionID: session})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SessionText(ctx context.Context, q protocol.TextQuery) (protocol.TextPage, error) {
	var out protocol.TextPage
	raw, err := s.action(ctx, protocol.OpSessionText, q)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SessionEnter(ctx context.Context, q protocol.EnterRequest) (protocol.EnterPlan, error) {
	var out protocol.EnterPlan
	raw, err := s.action(ctx, protocol.OpSessionEnter, q)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SetPeerTrust(ctx context.Context, session string, on bool) error {
	_, err := s.action(ctx, protocol.OpSessionTrust, protocol.SessionTrustRequest{SessionID: session, On: on})
	return err
}

func (s *Client) SetSessionPolicy(ctx context.Context, q protocol.SessionPolicyRequest) (protocol.SessionPolicyResult, error) {
	var out protocol.SessionPolicyResult
	raw, err := s.action(ctx, protocol.OpSessionPolicy, q)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) RenameNode(ctx context.Context, nodeID, name string) (protocol.NodeRenameResult, error) {
	var out protocol.NodeRenameResult
	raw, err := s.action(ctx, protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: nodeID, Name: name})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) Integrations(ctx context.Context) (protocol.IntegrationsResult, error) {
	var out protocol.IntegrationsResult
	raw, err := s.action(ctx, protocol.OpIntegrationsGet, nil)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SetIntegrations(ctx context.Context, in protocol.Integrations) (protocol.IntegrationsResult, error) {
	var out protocol.IntegrationsResult
	raw, err := s.action(ctx, protocol.OpIntegrationsSet, in)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) HiddenItems(ctx context.Context) (protocol.HiddenItems, error) {
	var out protocol.HiddenItems
	raw, err := s.action(ctx, protocol.OpHiddenGet, nil)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SetHiddenItems(ctx context.Context, in protocol.HiddenItems) (protocol.HiddenItems, error) {
	var out protocol.HiddenItems
	raw, err := s.action(ctx, protocol.OpHiddenSet, in)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) Counts(ctx context.Context) (protocol.SessionCounts, error) {
	var out protocol.SessionCounts
	raw, err := s.action(ctx, protocol.OpCounts, nil)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cl != nil {
		s.cl.Close()
		s.cl = nil
	}
}

// ResolveOutcome records an owner decision on a rejected or escalated request.
func (s *Client) ResolveOutcome(ctx context.Context, replyID, action string) error {
	_, err := s.action(ctx, "outcome_overrule", protocol.OutcomeResolution{ReplyID: replyID, Action: action})
	return err
}

func (s *Client) AnswerHeld(ctx context.Context, q protocol.HeldAnswer) error {
	_, err := s.action(ctx, protocol.OpHeldAnswer, q)
	return err
}

func (s *Client) SetRequestRule(ctx context.Context, e protocol.Edge) error {
	_, err := s.action(ctx, protocol.OpEdgeSet, e)
	return err
}
