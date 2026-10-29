// Package daemon is the per-machine process: it keeps one link to the gateway,
// tracks local sessions, delivers messages to them and serves the local socket.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
	"github.com/niparis/agent-net/internal/usage"
)

// Result is an adapter's report on one delivery.
// Delivered with Detail "unconfirmed" means a write failed after connecting:
// the message may have arrived, so it must not be retried.
type Result struct {
	Delivered bool
	Detail    string
}

// Adapter talks to one kind of agent harness that the daemon reaches by itself
// (no plugin connection). The Claude adapter is the real one.
type Adapter interface {
	Harness() string
	// Start begins discovery in the background. It reports sessions through h.
	Start(ctx context.Context, h Host) error
	Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result
}

// Host is what an adapter may ask of the daemon.
type Host interface {
	// SetSessions reports the full current set of sessions for one harness.
	SetSessions(harness string, infos []protocol.SessionInfo)
	// SessionID returns the gateway session ID, or "" if not known yet.
	SessionID(harness, nativeID string) string
	// Send posts a message to the network as the given sender session.
	Send(ctx context.Context, p protocol.MsgSend) error
	Logf(format string, args ...any)
}

type Config struct {
	GatewayURL string
	NodeID     string
	Token      string
	CAPEM      []byte // pinned CA; nil means the system roots (plain http in tests)
	Hostname   string
	StateDir   string
	SocketPath string
	// ClaudeSessionsDir is used by the peer check for `agent-net mcp`.
	ClaudeSessionsDir string
	Adapters          []Adapter
	Logger            *log.Logger

	BackoffMin, BackoffMax time.Duration
	// PluginAckTimeout is how long to wait for a plugin to confirm a push.
	PluginAckTimeout time.Duration

	// Version is the agent-net version of this process (sent in hello).
	Version string
	// Exe is the path of this binary, taken before it is ever replaced.
	// Default: the running executable.
	Exe string
	// MinLocalProtocol is the oldest local client protocol served.
	// Default: protocol.MinLocalProtocol.
	MinLocalProtocol int
	// Upgrade installs the version the gateway asks for and returns the
	// version that is now on disk. Default: download from the gateway.
	Upgrade func(ctx context.Context, target string) (from, to string, err error)
	// Restart restarts the daemon process after an upgrade. Default: the
	// service manager, or re-exec when there is none (see RestartSelf).
	Restart func()
	// NoAutoUpgrade turns off the upgrade to a newer version that the gateway
	// serves after each connect.
	NoAutoUpgrade bool
	// AutoUpgradeMaxDelay bounds the random wait before an automatic upgrade.
	// Default: 10 s; a negative value means no wait.
	AutoUpgradeMaxDelay time.Duration
	// Served returns the version the gateway serves to this machine.
	// Default: ask the gateway.
	Served func(ctx context.Context) (string, error)
	// RefreshAgentFiles runs once at start, before the gateway link, to bring
	// the Pi extension, the OpenCode plugin and the guidance blocks in line
	// with this binary. It logs through the function it gets. Default: none
	// (tests must not touch the home dir of the machine they run on).
	RefreshAgentFiles func(logf func(format string, args ...any))
	// MCPBackoffMin and MCPBackoffMax bound the restart delay of hosted MCP servers.
	MCPBackoffMin, MCPBackoffMax time.Duration
	// Repo finds the project of a session's folder. Default: repoinfo.New().
	Repo *repoinfo.Resolver
	// DetectHarnesses lists the installed harnesses. Default: harnesses.Detect
	// with the usual install folders.
	DetectHarnesses func(ctx context.Context) []protocol.HarnessInfo
	// HarnessInterval is how often DetectHarnesses runs after the first time.
	// Default: 6 hours.
	HarnessInterval time.Duration
	// UsageSampler measures CPU and memory (see usage.go). Nil turns that off.
	UsageSampler *usage.Sampler
	// UsageReportInterval is how often the summary goes to the gateway.
	// Default: 10 seconds.
	UsageReportInterval time.Duration
	// TextTimeout is how long to wait for a plugin to hand over a session's
	// conversation. Default: 8 seconds.
	TextTimeout time.Duration

	// NoLaunch makes this machine refuse workers (`agent-net setup --no-launch`).
	NoLaunch bool
	// Workers are the worker limits and timeouts. Zero fields take the defaults.
	Workers WorkerSettings
	// HarnessPaths overrides where a harness binary is, by harness name.
	// Default: the path from the inventory. Tests use it.
	HarnessPaths map[string]string
}

type localSession struct {
	info      protocol.SessionInfo
	sessionID string
	conn      *localConn // set for plugin sessions
}

func key(harness, nativeID string) string { return harness + "/" + nativeID }

type Daemon struct {
	cfg    Config
	log    *log.Logger
	st     *state
	client *http.Client

	mu       sync.Mutex
	sessions map[string]*localSession
	adapters map[string]Adapter
	inflight map[string]bool
	lk       *link
	// connecting is true between a successful dial and the end of the handshake.
	connecting bool
	// upserting counts session.upsert calls in flight. The gateway may push a
	// stored message right after the upsert result, before the session ID is
	// recorded here.
	upserting int

	sendMu  sync.Mutex
	lanesMu sync.Mutex
	lanes   map[string]*lane
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	ln      *localServer
	mcp     *mcpHost

	upg upgradeState

	// usage is the state of CPU and memory reporting (usage.go).
	usage usageState
	// wmu guards the workers started by this daemon (workers.go).
	wmu       sync.Mutex
	workers   map[string]*worker
	launchers map[string]launcher

	repo *repoinfo.Resolver
	// hmu guards the inventory of installed harnesses. hgen grows with each
	// new inventory, so the link can tell whether hello carried the latest.
	hmu       sync.Mutex
	installed []protocol.HarnessInfo // nil until the first detection ends
	hgen      int

	// cmdline is replaced in tests.
	cmdline func(pid int) string
	// exe returns the executable path of a process; replaced in tests.
	exe func(pid int) string
	// inSession tells whether a process is inside a local session's process
	// tree. Default: insideSession. Replaced in tests, where the "agent" and
	// the "command line" are the same process.
	inSession func(pid int) bool
}

// defaultDetect looks for the harnesses in the usual places. Tests replace it so
// they do not run the programs of the machine they run on.
var defaultDetect = func(ctx context.Context) []protocol.HarnessInfo {
	return harnesses.Detect(ctx, harnesses.Options{})
}

func New(cfg Config) (*Daemon, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	if cfg.BackoffMin == 0 {
		cfg.BackoffMin = time.Second
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 30 * time.Second
	}
	if cfg.PluginAckTimeout == 0 {
		cfg.PluginAckTimeout = 10 * time.Second
	}
	if cfg.ClaudeSessionsDir == "" {
		cfg.ClaudeSessionsDir = ClaudeSessionsDir()
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = SocketPath()
	}
	if cfg.StateDir == "" {
		cfg.StateDir = StateDir()
	}
	if cfg.Exe == "" {
		cfg.Exe, _ = os.Executable()
	}
	if cfg.Repo == nil {
		cfg.Repo = repoinfo.New()
	}
	if cfg.DetectHarnesses == nil {
		cfg.DetectHarnesses = defaultDetect
	}
	if cfg.HarnessInterval == 0 {
		cfg.HarnessInterval = 6 * time.Hour
	}
	st, err := openState(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	d := &Daemon{cfg: cfg, log: cfg.Logger, st: st, sessions: map[string]*localSession{},
		workers: map[string]*worker{}, launchers: map[string]launcher{"opencode": opencodeLauncher{}},
		adapters: map[string]Adapter{}, inflight: map[string]bool{}, lanes: map[string]*lane{}, cmdline: procCmdline, exe: procExe,
		client: http.DefaultClient}
	d.inSession = d.insideSession
	d.repo = cfg.Repo
	d.mcp = newMCPHost(d)
	if len(cfg.CAPEM) > 0 {
		c, err := PinnedClient(cfg.CAPEM)
		if err != nil {
			st.close()
			return nil, err
		}
		c.Timeout = 0 // the WebSocket lives for hours
		d.client = c
	}
	for _, a := range cfg.Adapters {
		d.adapters[a.Harness()] = a
	}
	d.addOwnerSession()
	return d, nil
}

// Start opens the local socket and runs the gateway link and the adapters in
// the background.
func (d *Daemon) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)
	if d.cfg.RefreshAgentFiles != nil {
		d.cfg.RefreshAgentFiles(d.Logf)
	}
	d.mcp.start(ctx)
	ln, err := listenLocal(d, d.cfg.SocketPath)
	if err != nil {
		d.cancel()
		return err
	}
	d.ln = ln
	d.wg.Add(1)
	go func() { defer d.wg.Done(); ln.serve(ctx) }()
	for _, a := range d.cfg.Adapters {
		if err := a.Start(ctx, d); err != nil {
			d.Stop()
			return fmt.Errorf("%s adapter: %w", a.Harness(), err)
		}
	}
	d.startUsage(ctx)
	d.adoptWorkers()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.workerLoop(ctx) }()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.harnessLoop(ctx) }()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.linkLoop(ctx) }()
	return nil
}

// Stop shuts everything down and waits.
func (d *Daemon) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	if d.ln != nil {
		d.ln.close()
	}
	d.mcp.stop()
	d.wg.Wait()
	d.st.close()
}

func (d *Daemon) Logf(format string, args ...any) { d.log.Printf(format, args...) }

// ---- registry ----

func (d *Daemon) SetSessions(harness string, infos []protocol.SessionInfo) {
	// The repository commands run here, outside the lock. A session whose other
	// fields changed gets a fresh branch; the rest use the cache.
	d.mu.Lock()
	before := map[string]protocol.SessionInfo{}
	for k, ls := range d.sessions {
		before[k] = ls.info
	}
	d.mu.Unlock()
	infos = append([]protocol.SessionInfo(nil), infos...)
	for i := range infos {
		infos[i].Harness = harness
		old, known := before[key(harness, infos[i].NativeID)]
		old.Repo, old.SessionID = protocol.RepoInfo{}, ""
		probe := infos[i]
		probe.Repo, probe.SessionID = protocol.RepoInfo{}, ""
		if known && old != probe {
			infos[i].Repo = d.repo.Refresh(infos[i].Cwd)
		} else {
			infos[i].Repo = d.repo.Lookup(infos[i].Cwd)
		}
	}
	d.mu.Lock()
	seen := map[string]bool{}
	var changed []*localSession
	var gone []string
	for _, in := range infos {
		in.Harness = harness
		k := key(harness, in.NativeID)
		seen[k] = true
		ls := d.sessions[k]
		if ls == nil {
			ls = &localSession{info: in}
			d.sessions[k] = ls
			changed = append(changed, ls)
		} else if !ls.plugin() && ls.info != in {
			sid := ls.info.SessionID
			ls.info = in
			ls.info.SessionID = sid
			changed = append(changed, ls)
		}
	}
	for k, ls := range d.sessions {
		if ls.info.Harness == harness && !ls.plugin() && !seen[k] {
			delete(d.sessions, k)
			if ls.sessionID != "" {
				gone = append(gone, ls.sessionID)
			}
		}
	}
	d.mu.Unlock()
	for _, ls := range changed {
		d.pushSession(ls)
	}
	for _, id := range gone {
		d.call(context.Background(), "session.remove", protocol.SessionRemove{SessionID: id})
	}
}

func (ls *localSession) plugin() bool { return ls.conn != nil }

func (d *Daemon) SessionID(harness, nativeID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ls := d.sessions[key(harness, nativeID)]; ls != nil {
		return ls.sessionID
	}
	return ""
}

func (d *Daemon) sessionByID(id string) *localSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ls := range d.sessions {
		if ls.sessionID == id {
			return ls
		}
	}
	return nil
}

func (d *Daemon) infoOf(ls *localSession) protocol.SessionInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ls.info
}

// pushSession tells the gateway about a session if the link is up.
func (d *Daemon) pushSession(ls *localSession) {
	info := d.infoOf(ls)
	d.mu.Lock()
	d.upserting++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.upserting--
		d.mu.Unlock()
	}()
	data, err := d.call(context.Background(), "session.upsert", protocol.SessionUpsert{Session: info})
	if err != nil {
		return
	}
	var r struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(data, &r) == nil && r.SessionID != "" {
		d.mu.Lock()
		ls.sessionID = r.SessionID
		d.mu.Unlock()
	}
}

func (d *Daemon) snapshot() []protocol.SessionInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]protocol.SessionInfo, 0, len(d.sessions))
	for _, ls := range d.sessions {
		out = append(out, ls.info)
	}
	sort.Slice(out, func(i, j int) bool {
		return key(out[i].Harness, out[i].NativeID) < key(out[j].Harness, out[j].NativeID)
	})
	return out
}

// Sessions lists local sessions with their gateway IDs.
func (d *Daemon) Sessions() []protocol.SessionInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]protocol.SessionInfo, 0, len(d.sessions))
	for _, ls := range d.sessions {
		in := ls.info
		in.SessionID = ls.sessionID
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		return key(out[i].Harness, out[i].NativeID) < key(out[j].Harness, out[j].NativeID)
	})
	return out
}

// ---- sending ----

var errNotConnected = errors.New("not connected to the gateway")

// RemoteError is a refusal from the gateway.
type RemoteError string

func (e RemoteError) Error() string { return string(e) }

// Send posts a message. When the gateway is unreachable the message goes to
// the outbox, with its idempotency key, and is sent on reconnect.
func (d *Daemon) Send(ctx context.Context, p protocol.MsgSend) error {
	_, err := d.send(ctx, p)
	return err
}

func (d *Daemon) send(ctx context.Context, p protocol.MsgSend) (json.RawMessage, error) {
	if p.IdempotencyKey == "" {
		p.IdempotencyKey = protocol.NewID("k")
	}
	d.touchWorker(p.Sender, p.Recipient) // a message to or from a worker keeps it alive
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	if d.st.outboxLen() == 0 {
		data, err := d.call(ctx, "msg.send", p)
		var re RemoteError
		if err == nil || errors.As(err, &re) {
			return data, err
		}
	}
	raw, _ := json.Marshal(p)
	if err := d.st.enqueue(raw); err != nil {
		return nil, err
	}
	d.Logf("gateway unreachable, queued message (key %s)", p.IdempotencyKey)
	out, _ := json.Marshal(map[string]any{"queued": true, "idempotency_key": p.IdempotencyKey})
	return out, nil
}

func (d *Daemon) flushOutbox(ctx context.Context) {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	items, err := d.st.outbox()
	if err != nil {
		d.Logf("outbox: %v", err)
		return
	}
	for _, q := range items {
		_, err := d.call(ctx, "msg.send", q.msg)
		var re RemoteError
		if err != nil && !errors.As(err, &re) {
			return // link trouble: keep the rest, in order
		}
		if err != nil {
			d.Logf("outbox message %s refused: %v", q.msg.IdempotencyKey, err)
		}
		d.st.dequeue(q.seq)
	}
}

// resolveTo turns a session ID or a unique online agent name into a session ID.
func (d *Daemon) resolveTo(ctx context.Context, to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("`to` is empty")
	}
	if strings.HasPrefix(to, "s_") {
		return to, nil
	}
	data, err := d.call(ctx, "query.agents", protocol.QueryAgents{OnlineOnly: true, Text: to})
	if err != nil {
		return "", err
	}
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	var match []protocol.AgentView
	for _, a := range r.Agents {
		if a.Profile.Name == to {
			match = append(match, a)
		}
	}
	switch len(match) {
	case 0:
		return "", fmt.Errorf("no online agent is named %q; use agent_list to find a session_id", to)
	case 1:
		return match[0].SessionID, nil
	}
	var lines []string
	for _, a := range match {
		lines = append(lines, fmt.Sprintf("%s (%s on %s, %s)", a.SessionID, a.Harness, a.Hostname, a.Cwd))
	}
	return "", fmt.Errorf("%d online agents are named %q, use a session_id: %s", len(match), to, strings.Join(lines, "; "))
}

// ---- delivery ----

func (d *Daemon) onDeliver(ctx context.Context, m protocol.Message) {
	// The gateway may push right after the sync result, before the handshake
	// here has recorded session IDs.
	for i := 0; i < 100 && d.isConnecting() && ctx.Err() == nil; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if d.st.isDelivered(m.ID) {
		d.sendUpdate(ctx, m.ID, protocol.StateDelivered, "duplicate")
		return
	}
	d.mu.Lock()
	if d.inflight[m.ID] {
		d.mu.Unlock()
		return
	}
	d.inflight[m.ID] = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.inflight, m.ID)
		d.mu.Unlock()
	}()

	ls := d.sessionByID(m.To)
	if ls == nil {
		d.sendUpdate(ctx, m.ID, protocol.StateFailed, "no such local session")
		return
	}
	d.touchWorker(m.To, m.From)
	d.noteAsk(m)
	res := d.deliverTo(ctx, ls, m)
	if !res.Delivered {
		d.sendUpdate(ctx, m.ID, protocol.StateFailed, res.Detail)
		return
	}
	if err := d.st.markDelivered(m.ID); err != nil {
		d.Logf("mark delivered: %v", err)
	}
	d.sendUpdate(ctx, m.ID, protocol.StateDelivered, res.Detail)
}

func (d *Daemon) deliverTo(ctx context.Context, ls *localSession, m protocol.Message) Result {
	info := d.infoOf(ls)
	info.SessionID = m.To
	d.mu.Lock()
	conn := ls.conn
	d.mu.Unlock()
	if conn != nil {
		return conn.push(ctx, m, d.cfg.PluginAckTimeout)
	}
	if protocol.IsOwner(info.Harness) {
		// The person reads it in the TUI feed. There is no program to push to.
		return Result{Delivered: true, Detail: "owner"}
	}
	a := d.adapters[info.Harness]
	if a == nil {
		return Result{Detail: "no adapter for harness " + info.Harness}
	}
	return a.Deliver(ctx, info, m)
}

func (d *Daemon) sendUpdate(ctx context.Context, id, state, detail string) {
	_, err := d.call(ctx, "delivery.update", protocol.DeliveryUpdate{MessageID: id, State: state, Detail: detail})
	if err != nil {
		d.Logf("delivery.update %s: %v", id, err)
	}
}

// Connected reports whether the gateway link is up and past its handshake.
func (d *Daemon) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lk != nil
}

// NodeID is the node identity from the credentials.
func (d *Daemon) NodeID() string { return d.cfg.NodeID }

func (d *Daemon) isConnecting() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connecting || d.upserting > 0
}

// lane runs deliveries to one session one at a time, in the order they arrived.
type lane struct {
	mu      sync.Mutex
	q       []func()
	running bool
}

func (d *Daemon) enqueue(laneKey string, fn func()) {
	d.lanesMu.Lock()
	l := d.lanes[laneKey]
	if l == nil {
		l = &lane{}
		d.lanes[laneKey] = l
	}
	d.lanesMu.Unlock()
	l.mu.Lock()
	l.q = append(l.q, fn)
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			l.mu.Lock()
			if len(l.q) == 0 {
				l.running = false
				l.mu.Unlock()
				return
			}
			f := l.q[0]
			l.q = l.q[1:]
			l.mu.Unlock()
			f()
		}
	}()
}
