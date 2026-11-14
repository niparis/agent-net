package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// Client is an Operator that talks to the local daemon over its Unix socket,
// as a CLI client. It connects again by itself after the daemon restarts.
type Client struct {
	path string
	via  string
	// screen names the tab or view that is on screen. A failed call is
	// reported with it (see reportFailure).
	screen atomic.Pointer[string]

	mu        sync.Mutex
	cl        *client.Client
	uiPush    func(protocol.UIRequest)
	topicSubs map[*TopicSubscription]bool
}

var _ Operator = (*Client)(nil)

// New returns a client for the daemon socket at path. It does not connect
// until the first call, so a daemon that starts later is found.
func New(path string) *Client { return &Client{path: path} }

// NewPhone uses the owner channel with a distinct accounting label.
func NewPhone(path string) *Client {
	c := &Client{path: path, via: protocol.ViaPhone}
	c.SetScreen("web")
	return c
}

// SetScreen says which tab or view makes the calls from now on. The daemon log
// and the gateway counters of failed calls show it.
func (s *Client) SetScreen(name string) {
	if cur := s.screen.Load(); cur != nil && *cur == name {
		return
	}
	s.screen.Store(&name)
}

func (s *Client) screenName() string {
	if p := s.screen.Load(); p != nil {
		return *p
	}
	return ""
}

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
	if _, err := cl.Call(ctx, "hello", map[string]any{"client": "cli", "via": s.via, "protocol": protocol.LocalProtocol, "tui": s.uiPush != nil}); err != nil {
		cl.Close()
		if client.IsConnError(err) {
			return nil, fmt.Errorf("%w (%s)", ErrDaemonDown, s.path)
		}
		return nil, err
	}
	s.cl = cl
	return cl, nil
}

// Call makes one daemon call and classifies the failure. A call that fails or
// times out is reported to the daemon, which logs it and counts it.
func (s *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	start := time.Now()
	raw, err := s.call(ctx, method, params)
	if err != nil {
		if s.reportFailure(ctx, method, start, err) {
			err = &ReportedError{Err: err}
		}
	}
	return raw, err
}

// ReportedError is an error from a call whose failure the client already sent
// to the daemon through client_error. A screen that shows the same failure
// must not report it a second time, so the mark travels with the error.
type ReportedError struct{ Err error }

func (e *ReportedError) Error() string { return e.Err.Error() }
func (e *ReportedError) Unwrap() error { return e.Err }

// IsReported says whether err came from a call whose failure is already in the
// daemon log.
func IsReported(err error) bool {
	var r *ReportedError
	return errors.As(err, &r)
}

func roundUpMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Millisecond - 1) / time.Millisecond)
}

type timeoutKey struct{}

// WithTimeout sets a deadline on parent and records the configured duration
// in the context so a timed-out call reports the exact timeout chosen by the
// caller rather than time remaining.
func WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	return context.WithValue(ctx, timeoutKey{}, timeout), cancel
}

// TimeoutFromContext returns the configured timeout carried in ctx, if any.
func TimeoutFromContext(ctx context.Context) (time.Duration, bool) {
	d, ok := ctx.Value(timeoutKey{}).(time.Duration)
	return d, ok
}

// reportFailure tells the daemon that a call failed on this side. The daemon
// cannot see a timeout that this side ended, so without the report it would
// leave no trace. It is not sent when there is nobody to tell (the daemon is
// down, or the gateway is, which the screen already shows), when the caller
// cancelled the call on purpose, for a report itself, or when the answer says
// an older daemon or gateway does not know the call (IsOldPeer): that is
// version skew, not a slow or failed request. It runs in the background and
// never changes the result of the call. It answers whether it will report, so
// the error can be marked as already in the log.
func (s *Client) reportFailure(ctx context.Context, method string, start time.Time, err error) bool {
	if method == protocol.OpClientError || errors.Is(err, context.Canceled) ||
		errors.Is(err, ErrDaemonDown) || errors.Is(err, ErrGatewayDown) || IsOldPeer(err.Error()) {
		return false
	}
	text := err.Error()
	report := protocol.ClientError{Method: method, Ms: time.Since(start).Milliseconds(), Screen: s.screenName(), Err: text,
		Timeout: errors.Is(err, context.DeadlineExceeded) || strings.Contains(text, "deadline exceeded")}
	if d, ok := TimeoutFromContext(ctx); ok {
		report.TimeoutMs = max(d.Milliseconds(), 0)
	} else if deadline, ok := ctx.Deadline(); ok {
		// The deadline was set a moment before start; round up so elapsed time
		// before start does not reduce the reported timeout by 1 ms.
		report.TimeoutMs = max(roundUpMs(deadline.Sub(start)), 0)
	}
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cl, err := s.conn(rctx)
		if err != nil {
			return
		}
		_, _ = cl.Call(rctx, protocol.OpClientError, report)
	}()
	return true
}

// ReportScreenError tells the daemon that the screen showed the owner an error
// line, with the session it belonged to when there is one. It goes through the
// same client_error report as a failed call, so the line reaches the daemon
// log and the gateway counters. It runs in the background and never changes
// what is on screen.
func (s *Client) ReportScreenError(text, sessionID string) {
	if text == "" {
		return
	}
	report := protocol.ClientError{
		Method: protocol.ScreenErrorMethod, Screen: s.screenName(),
		Text: text, SessionID: sessionID,
	}
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cl, err := s.conn(rctx)
		if err != nil {
			return
		}
		_, _ = cl.Call(rctx, protocol.OpClientError, report)
	}()
}

// ReportEnterFailure tells the daemon that a session Enter failed in the
// terminal it handed over, so the daemon log holds the command's last output.
// The daemon never sees that command end, so without this report the reason
// only exists on the TUI's screen. It runs in the background and never
// changes the result on screen.
func (s *Client) ReportEnterFailure(f protocol.EnterFailure) {
	if f.SessionID == "" {
		return
	}
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cl, err := s.conn(rctx)
		if err != nil {
			return
		}
		_, _ = cl.Call(rctx, protocol.OpEnterFailure, f)
	}()
}

func (s *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
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

// SnapshotOptions says which optional parts of a Snapshot to read. Snapshot
// reads them all; the screen uses this to leave out a part its current tab
// does not show.
type SnapshotOptions struct {
	// WorkerLog asks for the worker log, which only the Quotas workers log
	// shows.
	WorkerLog bool
	// Topics nil loads everything. Otherwise Previous supplies unchanged data.
	Topics   map[string]bool
	Previous Snapshot
	// IfVersions carries the topic version each part of Previous was read at.
	// A part whose topic has not moved is answered NotModified, so the caller
	// keeps its copy and the gateway sends no rows.
	IfVersions map[string]uint64
	// Lite leaves out fields no TUI list screen shows. The screen sets it; the
	// phone and the command line keep the full rows.
	Lite bool
}

func (s *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	return s.SnapshotWith(ctx, SnapshotOptions{WorkerLog: true})
}

// heldETag is the ETag to send for a part the caller holds. A part that holds
// no rows sends none, so the answer always carries rows.
func heldETag(holds bool, etag string) string {
	if !holds {
		return ""
	}
	return etag
}

// SnapshotWith is Snapshot with the optional parts chosen by the caller.
func (s *Client) SnapshotWith(ctx context.Context, opts SnapshotOptions) (Snapshot, error) {
	// A part the gateway reports unchanged, or one that fails, keeps the
	// caller's copy. This is what makes a safety refresh cheap.
	snap := opts.Previous
	snap.FailedCalls, snap.Unchanged = nil, nil
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
	snap.ProposalCount = info.ProposalCount
	// An older daemon does not say; it accepts workers only if it is new enough
	// to have the field, so "false" there is the safe reading.
	snap.Launch = info.Launch
	if !info.Connected {
		return snap, nil
	}
	// The independent calls share a connection but have separate deadlines.
	type request struct {
		method   string
		params   any
		required bool
		raw      json.RawMessage
		err      error
	}
	calls := []request{
		{method: protocol.OpAgents, params: protocol.QueryAgents{IncludeOwners: true, IfVersion: opts.IfVersions[protocol.TopicAgents], IfETag: heldETag(len(opts.Previous.Agents) > 0, opts.Previous.AgentsETag), WantETag: opts.Lite, Lite: opts.Lite}, required: true},
		{method: protocol.OpNodes, params: protocol.QueryNodes{IfVersion: opts.IfVersions[protocol.TopicNodes], IfETag: heldETag(len(opts.Previous.Nodes) > 0, opts.Previous.NodesETag), WantETag: opts.Lite}},
		{method: "notification_settings"}, {method: protocol.OpTimeZoneGet}, {method: protocol.OpWorkerProfiles},
		{method: protocol.OpWorkerProvidersGet}, {method: protocol.OpProtectedProvidersGet}, {method: protocol.OpWorkerFloorsGet},
		{method: protocol.OpScoresGet}, {method: "difficulty_get"}, {method: protocol.OpWorkerRotationGet},
		{method: "learned_list", params: protocol.LearnedListQuery{IfVersion: opts.IfVersions[protocol.TopicLearned], Lite: opts.Lite}},
		{method: protocol.OpGraphGet},
		{method: protocol.OpProjects, params: protocol.QueryProjects{}},
	}
	if opts.WorkerLog {
		calls = append(calls, request{method: protocol.OpWorkerLog, params: protocol.WorkerLogQuery{Limit: 200}})
	}
	if opts.Topics != nil {
		selected := calls[:0]
		for _, call := range calls {
			if opts.Topics[SnapshotTopic(call.method)] {
				selected = append(selected, call)
			}
		}
		calls = selected
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func(c *request) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				c.err = ctx.Err()
				return
			}
			defer func() { <-sem }()
			callCtx := ctx
			cancel := func() {}
			if !c.required {
				callCtx, cancel = WithTimeout(ctx, 3*time.Second)
			}
			defer cancel()
			c.raw, c.err = s.Call(callCtx, c.method, c.params)
		}(&calls[i])
	}
	wg.Wait()
	for _, c := range calls {
		var err = c.err
		if err == nil {
			switch c.method {
			case protocol.OpWorkerLog:
				err = json.Unmarshal(c.raw, &snap.WorkerLog)
			case protocol.OpAgents:
				var v protocol.AgentsResult
				err = json.Unmarshal(c.raw, &v)
				if v.NotModified {
					snap.Unchanged = append(snap.Unchanged, c.method)
				} else {
					snap.Agents, snap.AgentsETag = v.Agents, v.ETag
				}
			case "notification_settings":
				var v struct {
					Disabled bool `json:"disabled"`
				}
				err = json.Unmarshal(c.raw, &v)
				snap.NotificationsDisabled = v.Disabled
			case protocol.OpTimeZoneGet:
				var v protocol.TimeZoneSettings
				err = json.Unmarshal(c.raw, &v)
				snap.TimeZone = v.Name
			case protocol.OpNodes:
				var v protocol.NodesInfo
				err = json.Unmarshal(c.raw, &v)
				if v.NotModified {
					snap.Unchanged = append(snap.Unchanged, c.method)
				} else {
					snap.Nodes, snap.NodesETag = v.Nodes, v.ETag
					snap.Calibration = v.Calibration
					snap.GatewayWarnings = v.GatewayWarnings
				}
			case protocol.OpWorkerProfiles:
				err = json.Unmarshal(c.raw, &snap.WorkerProfiles)
			case protocol.OpWorkerProvidersGet:
				var v protocol.WorkerProviders
				err = json.Unmarshal(c.raw, &v)
				snap.WorkerProviders = v.Providers
			case protocol.OpProtectedProvidersGet:
				err = json.Unmarshal(c.raw, &snap.ProtectedProviders)
			case protocol.OpWorkerFloorsGet:
				err = json.Unmarshal(c.raw, &snap.WorkerFloors)
			case protocol.OpScoresGet:
				err = json.Unmarshal(c.raw, &snap.Scores)
			case "difficulty_get":
				err = json.Unmarshal(c.raw, &snap.DifficultyMinimums)
			case protocol.OpWorkerRotationGet:
				err = json.Unmarshal(c.raw, &snap.WorkerRotation)
			case "learned_list":
				err = json.Unmarshal(c.raw, &snap.Learned)
			case protocol.OpGraphGet:
				var v protocol.Graph
				err = json.Unmarshal(c.raw, &v)
				snap.Edges = v.Edges
			case protocol.OpProjects:
				var v protocol.ProjectsResult
				err = json.Unmarshal(c.raw, &v)
				snap.Projects = v.Projects
			}
		}
		if err != nil {
			if c.required {
				return snap, err
			}
			snap.FailedCalls = append(snap.FailedCalls, c.method)
		}
	}

	return snap, nil
}

// SnapshotTopic maps each snapshot read to the dependency versions that make
// it stale. Local settings are refreshed with workers on a safety refresh too.
// A caller keeps the version of a part from the fetch that read it.
func SnapshotTopic(method string) string {
	switch method {
	case protocol.OpAgents:
		return protocol.TopicAgents
	case protocol.OpNodes:
		return protocol.TopicNodes
	case protocol.OpWorkerLog, protocol.OpWorkerProfiles, protocol.OpWorkerProvidersGet,
		protocol.OpProtectedProvidersGet, protocol.OpWorkerFloorsGet, protocol.OpScoresGet,
		"difficulty_get", protocol.OpWorkerRotationGet, "notification_settings", protocol.OpTimeZoneGet:
		return protocol.TopicWorkers
	case "learned_list":
		return protocol.TopicLearned
	case protocol.OpGraphGet:
		return protocol.TopicGraph
	case protocol.OpProjects:
		return protocol.TopicProjects
	}
	return ""
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

func (s *Client) Conversations(ctx context.Context, q protocol.QueryConversations) (protocol.ConversationPage, error) {
	var out protocol.ConversationPage
	raw, err := s.action(ctx, protocol.OpConversations, q)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) ConversationMessages(ctx context.Context, conversationID string) ([]protocol.Message, error) {
	var out protocol.ConversationMessagesResult
	raw, err := s.action(ctx, protocol.OpConversationMessages, protocol.QueryConversationMessages{ConversationID: conversationID, Limit: 200})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Messages, nil
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

// ReplyAsOwner sends a message from the owner into a stored conversation. The
// gateway keeps it in that conversation when To is one of its sides.
func (s *Client) ReplyAsOwner(ctx context.Context, conversationID, to, body string) (protocol.MessageSent, error) {
	var out protocol.MessageSent
	raw, err := s.action(ctx, protocol.OpSend, protocol.OwnerSendRequest{To: to, Body: body, ConversationID: conversationID, IdempotencyKey: protocol.NewID("k")})
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

func (s *Client) ReportDefect(ctx context.Context, worker, note string, severity int) (protocol.RateResult, error) {
	var out protocol.RateResult
	raw, err := s.action(ctx, protocol.OpWorkerDefect, protocol.DefectRequest{Worker: worker, Severity: severity, Note: note})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SessionStart(ctx context.Context, req protocol.SessionStartRequest) (protocol.StartedSession, error) {
	var out protocol.StartedSession
	raw, err := s.action(ctx, protocol.OpSessionStart, req)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SessionResume(ctx context.Context, req protocol.SessionResumeRequest) (protocol.StartedSession, error) {
	var out protocol.StartedSession
	raw, err := s.action(ctx, protocol.OpSessionResume, req)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) SessionStartGet(ctx context.Context, id string) (protocol.StartedSession, error) {
	var out protocol.StartedSession
	raw, err := s.action(ctx, protocol.OpSessionStartGet, protocol.SessionStartQuery{ID: id})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
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

func (s *Client) MachineAction(ctx context.Context, nodeID, action string) error {
	if action != protocol.OpMachineRemove && action != protocol.OpMachineRotate {
		return fmt.Errorf("unknown machine action %q", action)
	}
	_, err := s.action(ctx, action, map[string]string{"node_id": nodeID})
	return err
}

func (s *Client) RenameNode(ctx context.Context, nodeID, name string) (protocol.NodeRenameResult, error) {
	var out protocol.NodeRenameResult
	raw, err := s.action(ctx, protocol.OpNodeRename, protocol.NodeRenameRequest{NodeID: nodeID, Name: name})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *Client) RenameSession(ctx context.Context, sessionID, name string) (protocol.SessionRenameResult, error) {
	var out protocol.SessionRenameResult
	raw, err := s.action(ctx, protocol.OpSessionRename, protocol.SessionRenameRequest{SessionID: sessionID, Name: name})
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
	for sub := range s.topicSubs {
		sub.Close()
	}
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

func (s *Client) ProjectDisplayName(ctx context.Context, project string) (protocol.ProjectDisplayResult, error) {
	var out protocol.ProjectDisplayResult
	raw, err := s.action(ctx, protocol.OpProjectDisplayGet, protocol.ProjectDisplayRequest{Project: project})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}
func (s *Client) SetProjectDisplayName(ctx context.Context, project, name string) (protocol.ProjectDisplayResult, error) {
	var out protocol.ProjectDisplayResult
	raw, err := s.action(ctx, protocol.OpProjectDisplaySet, protocol.ProjectDisplayRequest{Project: project, Name: name})
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

// NotificationSettings reads or updates the local owner's desktop preference.
func (s *Client) NotificationSettings(ctx context.Context, disabled *bool) (bool, error) {
	raw, err := s.action(ctx, "notification_settings", map[string]any{"disabled": disabled})
	if err != nil {
		return false, err
	}
	var out struct {
		Disabled bool `json:"disabled"`
	}
	err = json.Unmarshal(raw, &out)
	return out.Disabled, err
}

// TimeZoneSettings reads the owner's display time zone for the whole network,
// or writes it when name is not nil. Owner only.
func (s *Client) TimeZoneSettings(ctx context.Context, name *string) (protocol.TimeZoneSettings, error) {
	params := map[string]any{}
	method := protocol.OpTimeZoneGet
	if name != nil {
		params["name"] = *name
		method = protocol.OpTimeZoneSet
	}
	raw, err := s.action(ctx, method, params)
	if err != nil {
		return protocol.TimeZoneSettings{}, err
	}
	var out protocol.TimeZoneSettings
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *Client) SessionStop(ctx context.Context, session string) (string, error) {
	raw, err := s.action(ctx, protocol.OpSessionStop, protocol.SessionResumeRequest{SessionID: session})
	if err != nil {
		return "", err
	}
	var out protocol.SessionStopResult
	err = json.Unmarshal(raw, &out)
	return out.Note, err
}
