// Package gateway is the network server: enrollment, the node WebSocket,
// routing and storage.
package gateway

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/linkkeepalive"
	"github.com/niparis/agent-net/internal/phrasing"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/ratelimit"
	"github.com/niparis/agent-net/internal/tsnode"
)

type Options struct {
	DataDir string
	// PublicURL is the externally reachable base URL for connected apps.
	PublicURL string
	// AppsListen overrides the managed loopback address. Empty uses 127.0.0.1:7422.
	AppsListen string
	// PhoneApp is the phone web app, served at "/" on the apps listener.
	// Nil serves no phone app.
	PhoneApp http.Handler
	// FunnelRunner is injectable so tests never execute tailscale.
	FunnelRunner funnel.Runner
	// TailscaleNode opens agent-net's own Tailscale node when the owner chose
	// one in setup. Nil uses the real node; tests supply a fake.
	TailscaleNode tsnode.Opener
	// DistDir holds the agent-net-{os}-{arch} binaries that /install serves.
	DistDir string
	// SANs are extra host names or IPs for the server certificate.
	SANs []string
	// OfflineGrace is how long a node may be disconnected before its
	// sessions go offline. Default and maximum 10 s.
	OfflineGrace time.Duration
	// KeepalivePingInterval and KeepaliveTimeout control node link health checks.
	// Zero values use 20 seconds and 45 seconds respectively.
	KeepalivePingInterval, KeepaliveTimeout time.Duration
	Logger                                  *log.Logger
	// Now is for tests.
	Now func() time.Time
	// ToolsPageSize limits the item count per page for tests. Zero uses the normal byte limit.
	ToolsPageSize int
	// Version is the agent-net version this gateway runs (served at
	// /install/version and listed with the nodes).
	Version string
	// Restart restarts the gateway process. The admin restart call uses it
	// when there is no service manager. Nil means that call is refused.
	Restart func()
	// Retention says how long data is kept. The zero value keeps everything.
	Retention Retention
	// TempFolders lists folders whose sessions belong to no project. Nil uses
	// the system temporary folders; an empty list turns the rule off (tests).
	TempFolders []string
	// StepTaskQuietWindow is how long to wait with no new tasks before
	// flushing a batched step task message. Default 10s.
	StepTaskQuietWindow time.Duration
	// StepTaskRecentWindow is how long after a step task message to coalesce
	// new tasks into a pending batch. Default 30s.
	StepTaskRecentWindow time.Duration
	// StepTaskBatchCap is the maximum number of task lines in one batch message.
	// Default 25.
	StepTaskBatchCap int
}

type Gateway struct {
	// previewMemo keeps the worker-choice preview of each node (NodesInfo).
	previewMemo previewMemo
	// relayWait overrides defaultRelayWait, for tests.
	relayWait    time.Duration
	publicMu     sync.Mutex
	publicURLMu  sync.RWMutex
	publicURL    string
	public       protocol.AppsPublicStatus
	publicServer *http.Server
	publicAddr   string
	// tsChoice is the owner's own-node choice (decision 0058); nil keeps the
	// machine's Tailscale node. tsNode and tsServer are that node and the
	// server on its Funnel listener. All three are guarded by publicMu.
	tsChoice        *tsnode.Choice
	tsNode          tsnode.Node
	tsServer        *http.Server
	limiter         *ratelimit.Limiter
	opts            Options
	store           *Store
	skillsMu        sync.Mutex
	skillProjectsMu sync.Mutex
	// standing* run addNewSkillProjects in the background, one run at a time.
	standingMu      sync.Mutex
	standingRunning bool
	standingPending bool
	standingStopped bool
	standingCancel  context.CancelFunc
	standingCtx     context.Context
	standingWG      sync.WaitGroup
	// onStandingPlan is a test hook, called before each standing-choice plan.
	onStandingPlan func(item string, projects []string)
	// importMu serializes MCP imports, so two applies cannot race on a name.
	importMu         sync.Mutex
	skills           *skillStore
	ca               *authority
	cert             tls.Certificate
	fp               string
	pin              string
	logger           *log.Logger
	stepDispatchRuns atomic.Int64
	// skillScans counts the full reads of all skills by the standing choice.
	skillScans atomic.Int64
	// skillSettled is the skill inputs fingerprint at which the standing
	// choice had nothing left to do. See skillInputsOf.
	skillSettled atomic.Pointer[skillInputs]
	// adminToken guards /v1/admin. It is new on every start and is written
	// to admin.json in the data dir, which only the local user can read.
	adminToken string

	// usage holds the newest CPU and memory report of each node (usage.go).
	usage usageTable

	// reqs counts the request frames by method and origin (request_stats.go).
	// It is nil in a Gateway not made by New.
	reqs *requestStats

	// Step task batching and coalescing.
	stepTaskQuietWindow  time.Duration
	stepTaskRecentWindow time.Duration
	stepTaskBatchCap     int
	stepBatchesMu        sync.Mutex
	stepBatches          map[string]*stepTaskBatch
	stepBatchGen         uint64 // under stepBatchesMu
	lastReportMu         sync.Mutex
	lastReportBySession  map[string]time.Time
	// onStepBatchFlushed is a test hook, called after a batch leaves the map
	// and before its message is stored, to reproduce a dispatch that overlaps
	// the quiet-window flush.
	onStepBatchFlushed func(session string)

	mu                sync.Mutex
	conns             map[string]*nodeConn
	connVersion       atomic.Uint64
	wakeMu            sync.Mutex
	wakeClaims        map[string]wakeClaim
	timers            map[string]*offlineTimer
	closed            bool
	appsDone          chan struct{}
	rotationMu        sync.Mutex
	machineRotationMu sync.Mutex
	machineAuthMu     sync.Mutex // authentication, removal and confirmation ordering
	// autoCutover is set on a fully built gateway. Partial gateways in tests
	// never start the automatic typed Graph cutover.
	autoCutover bool
	// graphCutoverMu coalesces the automatic cutover checks so a burst of
	// hellos starts one run and leaves one follow-up pending.
	graphCutoverMu      sync.Mutex
	graphCutoverRunning bool
	graphCutoverPending bool
	// graphCutoverClosing is set when Close begins: no cutover starts after it.
	graphCutoverClosing bool
	// graphCutoverWG holds the running cutover goroutine. Close waits for it, so
	// the database never closes under a backup or a transaction.
	graphCutoverWG sync.WaitGroup
	// graphCutoverCheckMu serializes the cutover checks themselves.
	graphCutoverCheckMu sync.Mutex
	// bg* hold the short background jobs a write starts (the settings and
	// charter broadcasts). Close stops them and waits for them (background.go).
	bgMu      sync.Mutex
	bgStopped bool
	bgCtx     context.Context
	bgCancel  context.CancelFunc
	bgWG      sync.WaitGroup
	// transferMu keeps the parts of every xfer.* frame in order.
	transferMu sync.Mutex

	enrollLimit      *enrollLimiter
	exposureWarnings []string
	exposureMu       sync.RWMutex
}

type offlineTimer struct {
	timer *time.Timer
}

type nodeConn struct {
	topicSubscribed atomic.Bool
	sessionAgents   atomic.Bool
	graphRefs       atomic.Bool
	id              string
	tokenHash       string // credential used for this connection
	ws              *websocket.Conn
	wmu             sync.Mutex
	cancel          context.CancelFunc
	dead            chan struct{} // closed when the connection ends
	health          *linkkeepalive.Monitor

	pmu     sync.Mutex
	nextID  int
	pending map[string]chan protocol.Header
}

// errNodeGone means the connection to the node ended before it answered.
var errNodeGone = errors.New("node is not connected")

// call sends a request to the node and waits for its result frame.
func (nc *nodeConn) call(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	nc.pmu.Lock()
	nc.nextID++
	id := "g" + strconv.Itoa(nc.nextID)
	ch := make(chan protocol.Header, 1)
	if nc.pending == nil {
		nc.pending = map[string]chan protocol.Header{}
	}
	nc.pending[id] = ch
	nc.pmu.Unlock()
	defer func() {
		nc.pmu.Lock()
		delete(nc.pending, id)
		nc.pmu.Unlock()
	}()
	frame, err := protocol.EncodeRequest(typ, id, params)
	if err != nil {
		return nil, err
	}
	if err := nc.write(ctx, frame); err != nil {
		return nil, errNodeGone
	}
	select {
	case <-nc.dead:
		return nil, errNodeGone
	case h := <-ch:
		if !h.OK {
			return nil, errors.New(h.Error)
		}
		return h.Data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// answer hands a result frame to the call that waits for it.
func (nc *nodeConn) answer(h protocol.Header) {
	nc.pmu.Lock()
	ch := nc.pending[h.ID]
	nc.pmu.Unlock()
	if ch != nil {
		select {
		case ch <- h:
		default:
		}
	}
}

// write sends one frame. A connection with no websocket (a test fake, or one
// that is not wired up) cannot be written to; every broadcast walks the whole
// connection table, so this says so with an error and does not crash them.
func (nc *nodeConn) write(ctx context.Context, b []byte) error {
	if nc.ws == nil {
		return errNodeGone
	}
	nc.wmu.Lock()
	defer nc.wmu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return nc.ws.Write(ctx, websocket.MessageText, b)
}

// New opens the data dir, creating the database and the CA when missing.
func New(opts Options) (*Gateway, error) {
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	if opts.OfflineGrace == 0 || opts.OfflineGrace > 10*time.Second {
		opts.OfflineGrace = 10 * time.Second
	}
	if opts.KeepalivePingInterval <= 0 {
		opts.KeepalivePingInterval = linkkeepalive.DefaultPingInterval
	}
	if opts.KeepaliveTimeout <= 0 {
		opts.KeepaliveTimeout = linkkeepalive.DefaultTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	store, err := OpenStore(filepath.Join(opts.DataDir, "gateway.db"))
	if err != nil {
		return nil, err
	}
	if opts.Now != nil {
		store.now = opts.Now
	}
	if opts.TempFolders != nil {
		store.tempFolders = opts.TempFolders
	}
	if _, err := store.noteGatewayVersion(opts.Version, store.now()); err != nil {
		store.Close()
		return nil, err
	}
	if err := store.SetAllOffline(); err != nil {
		store.Close()
		return nil, err
	}
	ca, err := loadOrCreateCA(opts.DataDir)
	if err != nil {
		store.Close()
		return nil, err
	}
	serverKey, err := loadOrCreateServerKey(opts.DataDir)
	if err != nil {
		store.Close()
		return nil, err
	}
	pin, err := PublicKeyPin(&serverKey.PublicKey)
	if err != nil {
		store.Close()
		return nil, err
	}
	cert, err := ca.serverCert(serverKey, opts.SANs)
	if err != nil {
		store.Close()
		return nil, err
	}
	fp, err := Fingerprint(ca.certPEM)
	if err != nil {
		store.Close()
		return nil, err
	}
	g := &Gateway{limiter: ratelimit.New(ratelimit.Config{}, opts.Now), opts: opts, store: store, ca: ca, cert: cert, fp: fp, pin: pin, logger: logger, autoCutover: true, reqs: newRequestStats(time.Now()),
		appsDone: make(chan struct{}), adminToken: protocol.NewToken(), conns: map[string]*nodeConn{}, timers: map[string]*offlineTimer{},
		enrollLimit: newEnrollLimiter(opts.Now),
		stepBatches: make(map[string]*stepTaskBatch), lastReportBySession: make(map[string]time.Time),
		stepTaskQuietWindow: opts.StepTaskQuietWindow, stepTaskRecentWindow: opts.StepTaskRecentWindow, stepTaskBatchCap: opts.StepTaskBatchCap}
	store.settingsHook = func() { g.goBackground(g.settingsChanged) }
	store.charterHook = func() { g.goBackground(g.charterChanged) }
	store.phraseCleaner = phrasing.New(phraseWorker{g}, phraseLimiter{store})
	skills, err := openSkillStore(opts.DataDir, store)
	if err != nil {
		store.Close()
		return nil, err
	}
	g.skills = skills
	store.logf = g.logLine
	for _, merge := range store.identityMerges {
		g.logLine("project.identity_merge", "from", merge[0], "to", merge[1])
	}
	for _, conflict := range store.identityConflicts {
		g.logLine("project.identity_conflict", "from", conflict[0], "to", conflict[1])
	}
	if store.graphJunkMigrated {
		g.logLine("graph.junk_removed", "count", store.graphJunkRemoved)
	}
	if err := g.restorePublic(); err != nil {
		store.Close()
		return nil, err
	}
	return g, nil
}

func (g *Gateway) Store() *Store       { return g.store }
func (g *Gateway) Fingerprint() string { return g.fp }
func (g *Gateway) Pin() string         { return g.pin }
func (g *Gateway) CAPEM() []byte       { return g.ca.certPEM }
func (g *Gateway) TLSConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{g.cert}, MinVersion: tls.VersionTLS12}
}

// Close drops every node connection and closes the database.
func (g *Gateway) Close() error {
	g.stopStandingChoices()
	// The database and the connections are still open here, so a broadcast that
	// is running finishes or stops on its cancelled context.
	g.stopBackground()
	g.publicMu.Lock()
	g.stopPublicListener()
	g.closeTailscaleNode()
	g.publicMu.Unlock()
	g.mu.Lock()
	if !g.closed && g.appsDone != nil {
		close(g.appsDone)
	}
	g.closed = true
	for _, t := range g.timers {
		t.timer.Stop()
	}
	g.stepBatchesMu.Lock()
	for _, b := range g.stepBatches {
		if b.timer != nil {
			b.timer.Stop()
		}
	}
	g.stepBatches = nil
	g.stepBatchesMu.Unlock()
	conns := make([]*nodeConn, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		c.ws.Close(websocket.StatusGoingAway, "gateway closing")
		c.cancel()
	}
	g.graphCutoverMu.Lock()
	g.graphCutoverClosing = true
	g.graphCutoverMu.Unlock()
	closeAllAppSessions(g.store)
	// Give the read loops a moment to leave before the database goes away.
	time.Sleep(50 * time.Millisecond)
	// A cutover in its backup or transaction finishes first, so the backup is
	// complete and the database is still open for it.
	g.graphCutoverWG.Wait()
	return g.store.Close()
}

func (g *Gateway) autoArchiveSessions() {
	ids, err := g.store.AutoArchiveSessions()
	if err != nil {
		g.logLine("error", "where", "auto archive sessions", "err", err)
		return
	}
	for _, id := range ids {
		g.logLine("session.auto_archived", "session", id)
	}
}

// RunExpiry expires overdue messages once a minute until ctx ends.
func (g *Gateway) RunExpiry(ctx context.Context) {
	go g.RunRetries(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	agentStarts := time.NewTicker(time.Second)
	defer agentStarts.Stop()
	appsCleanup := time.NewTicker(10 * time.Minute)
	defer appsCleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.appsDone:
			return
		case <-agentStarts.C:
			func() {
				defer g.slowLoop("agent_starts", time.Now())
				nodes, err := g.store.ExpireSessionAgentStarts()
				if err != nil {
					g.logLine("error", "where", "expire Agent starts", "err", err)
				}
				for _, node := range nodes {
					g.dispatchPending(ctx, node, false)
				}
				// The chief of staff's asks go out in batches (decision 0114).
				g.flushChief(ctx)
			}()
		case <-appsCleanup.C:
			func() {
				defer g.slowLoop("apps_cleanup", time.Now())
				now := g.store.now()
				if err := g.store.DeleteExpiredTokens(now); err != nil {
					g.logLine("error", "where", "app token cleanup", "err", err)
				}
				if err := g.store.CleanupOAuth(now); err != nil {
					g.logLine("error", "where", "oauth cleanup", "err", err)
				}
			}()
		case <-t.C:
			func() {
				defer g.slowLoop("expiry", time.Now())
				g.logStepDispatchCount()
				g.retryPublicConsent(ctx)
				if err := g.store.checkSessionStalls(); err != nil {
					g.logLine("error", "where", "session stalls", "err", err)
				}
				if err := g.store.reconcileAsks(); err != nil {
					g.logLine("error", "where", "asks", "err", err)
				}
				g.flushLeadRequests(ctx)
				if err := g.store.samplePendingStalls(); err != nil {
					g.logLine("error", "where", "pending sample", "err", err)
				}
				reminders, err := g.store.remindPending()
				if err != nil {
					g.logLine("error", "where", "pending reminders", "err", err)
				}
				for _, m := range reminders {
					g.dispatch(ctx, m)
				}
				if len(reminders) > 0 {
					_ = g.store.deskLog(g.store.db, "gateway", "", "", fmt.Sprintf("Reminded %d sessions about messages they have not answered", len(reminders)), "", nil)
				}
				g.loopTick(ctx)
				checks, err := g.store.checkIdle()
				if err != nil {
					g.logLine("error", "where", "idle checks", "err", err)
				}
				for _, m := range checks {
					g.dispatch(ctx, m)
				}
				if len(checks) > 0 {
					_ = g.store.deskLog(g.store.db, "gateway", "", "", fmt.Sprintf("Asked %d idle sessions whether they are done", len(checks)), "", nil)
				}
				startNotices, err := g.store.ExpireStarts(0, 0)
				if err != nil {
					g.logLine("error", "where", "expire starts", "err", err)
				}
				for _, m := range startNotices {
					g.dispatch(ctx, m)
				}
				// Expire writes one log line per message.
				if n, err := g.store.Expire(); err != nil {
					g.logLine("error", "where", "expiry", "err", err)
				} else if n > 0 {
					g.mu.Lock()
					nodes := make([]string, 0, len(g.conns))
					for id := range g.conns {
						nodes = append(nodes, id)
					}
					g.mu.Unlock()
					for _, id := range nodes {
						g.dispatchPending(ctx, id, false)
					}
				}
				g.dispatchStepTasks(ctx)
			}()
		}
	}
}

// RunLearning processes suggestions independently of message expiry because a
// phrasing worker can take up to three minutes to finish.
func (g *Gateway) RunLearning(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		func() {
			defer g.slowLoop("learning", time.Now())
			if err := g.store.ProcessLearnedContext(ctx); err != nil && ctx.Err() == nil {
				g.logLine("error", "where", "learned suggestions", "err", err)
			}
		}()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RetentionInterval is how often RunRetention deletes old data.
const RetentionInterval = time.Hour

// prune deletes data past retention. A merger session removed by retention
// frees its tasks, so the caller learns about them now rather than at the next
// delivery change.
func (g *Gateway) prune(ctx context.Context) (PruneResult, error) {
	return g.pruneWithRetention(ctx, g.opts.Retention)
}

func (g *Gateway) pruneWithRetention(ctx context.Context, retention Retention) (PruneResult, error) {
	res, err := g.store.Prune(retention)
	if err != nil {
		return res, err
	}
	if res.Sessions > 0 {
		g.dispatchStepTasks(ctx)
	}
	return res, nil
}

// RunRetention deletes data older than Options.Retention, once at start and
// then every RetentionInterval, until ctx ends.
func (g *Gateway) RunRetention(ctx context.Context) {
	t := time.NewTicker(RetentionInterval)
	defer t.Stop()
	first := true
	for {
		func() {
			defer g.slowLoop("retention", time.Now())
			// At startup all sessions are marked offline until their daemons
			// reconnect. Wait for the first maintenance interval before archiving.
			retention := g.opts.Retention
			if first {
				// Preserve offline records until the archive has had its first pass.
				retention.Sessions = 0
			} else {
				g.autoArchiveSessions()
			}
			first = false
			if _, err := g.pruneWithRetention(ctx, retention); err != nil {
				g.logLine("error", "where", "retention", "err", err)
			}
			// A vacuum rewrites gateway.db. It runs at most once a week and only
			// when deleted rows left more than a fifth of the file free. It never
			// deletes messages.
			before, after, ran, err := g.store.MaybeVacuum(g.store.now())
			if err != nil {
				g.logLine("error", "where", "vacuum", "err", err)
			} else if ran {
				g.logLine("db.vacuum", "before", before, "after", after)
			}
			g.sweepTransfersNow()
		}()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ca", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(g.ca.certPEM)
	})
	mux.HandleFunc("POST /v1/enroll", g.handleEnroll)
	mux.HandleFunc("GET /v1/node", g.handleNode)
	mux.HandleFunc("GET /v1/quality-settings", g.handleQualitySettings)
	mux.HandleFunc("PUT /v1/quality-settings", g.handleQualitySettings)
	mux.HandleFunc("GET /v1/timezone-settings", g.handleTimeZoneSettings)
	mux.HandleFunc("PUT /v1/timezone-settings", g.handleTimeZoneSettings)
	mux.HandleFunc("GET /v1/protected-providers", g.handleProtectedProviders)
	mux.HandleFunc("PUT /v1/protected-providers", g.handleProtectedProviders)
	mux.HandleFunc("GET /v1/worker-floors", g.handleWorkerFloors)
	mux.HandleFunc("PUT /v1/worker-floors", g.handleWorkerFloors)
	mux.HandleFunc("GET /v1/worker-providers", g.handleWorkerProviders)
	mux.HandleFunc("PUT /v1/worker-providers", g.handleWorkerProviders)
	mux.HandleFunc("GET /v1/difficulty", g.handleDifficulty)
	mux.HandleFunc("PUT /v1/difficulty", g.handleDifficulty)
	mux.HandleFunc("GET /v1/scores", g.handleScoresGet)
	mux.HandleFunc("PUT /v1/scores", g.handleScoresSet)
	mux.HandleFunc("GET /v1/worker-rotation", g.handleWorkerRotation)
	mux.HandleFunc("GET /v1/picker", g.handlePicker)
	mux.HandleFunc("GET /v1/cto-settings", g.handleCTOSettingsGet)
	mux.HandleFunc("PUT /v1/cto-settings", g.handleCTOSettingsSet)
	mux.HandleFunc("GET /v1/integrations", g.handleIntegrationsGet)
	mux.HandleFunc("PUT /v1/integrations", g.handleIntegrationsSet)
	mux.HandleFunc("GET /v1/cto-title", g.handleCTOTitleGet)
	mux.HandleFunc("PUT /v1/cto-title", g.handleCTOTitleSet)
	mux.HandleFunc("GET /install/bin/{os}/{arch}", g.handleInstallBin)
	mux.HandleFunc("GET /install/{code}", g.handleInstallScript)
	g.upgradeRoutes(mux)
	g.recordRoutes(mux)
	return mux
}

func (g *Gateway) handleEnroll(w http.ResponseWriter, r *http.Request) {
	remote := ratelimit.ClientAddr(r)
	if allowed, shouldLog := g.enrollLimit.Allow(remote); !allowed {
		if shouldLog {
			g.logLine("enroll.rate_limited", "source", remote)
		}
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	var req struct {
		Code     string `json:"code"`
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ok, err := g.store.ConsumeEnrollCode(req.Code)
	if err != nil {
		g.logLine("error", "where", "enroll", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		g.store.Event("enroll.refused", "", "", "", req.Hostname)
		http.Error(w, "invalid, expired or used enrollment code", http.StatusForbidden)
		return
	}
	id, token, err := g.store.CreateNode(req.Hostname, req.OS)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"node_id": id, "token": token})
}

func (g *Gateway) handleNode(w http.ResponseWriter, r *http.Request) {
	g.machineAuthMu.Lock()
	if g.store.machineRemoved(r.Header.Get("X-Agent-Net-Node-ID")) {
		g.machineAuthMu.Unlock()
		w.Header().Set("Agent-Net-Removed", "true")
		http.Error(w, "this machine was removed from the network; run agent-net setup to join again", http.StatusGone)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	nodeID, ok, err := g.store.NodeByToken(token)
	if err != nil || !ok || token == "" {
		g.machineAuthMu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	health := linkkeepalive.New()
	ws, err := websocket.Accept(health.ResponseWriter(w), r, &websocket.AcceptOptions{
		OnPingReceived: func(context.Context, []byte) bool { health.Received(); return true },
		OnPongReceived: func(context.Context, []byte) { health.Received() },
	})
	if err != nil {
		g.machineAuthMu.Unlock()
		return
	}
	ws.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	nc := &nodeConn{id: nodeID, tokenHash: hashHex(token), ws: ws, cancel: cancel, dead: make(chan struct{}), health: health}
	// After cutover require a fresh hello before routing. Its capability
	// controls Graph access, but older daemons can still connect and upgrade.
	if on, err := g.store.graphRefsEnabled(); err != nil || on {
		if err != nil {
			g.machineAuthMu.Unlock()
			cancel()
			ws.CloseNow()
			return
		}
		hctx, done := context.WithTimeout(ctx, 10*time.Second)
		_, raw, err := nc.health.Read(hctx, ws)
		var h protocol.Header
		var result any
		if err == nil {
			if json.Unmarshal(raw, &h) != nil || h.Type != "hello" {
				err = reject("upgrade required: typed Graph hello required")
			} else {
				result, err = g.handle(hctx, nc, h.Type, raw)
			}
			reply, _ := protocol.EncodeResult(h.ID, result, err)
			_ = nc.write(hctx, reply)
		}
		done()
		if err != nil {
			g.machineAuthMu.Unlock()
			cancel()
			ws.CloseNow()
			return
		}
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		g.machineAuthMu.Unlock()
		ws.Close(websocket.StatusGoingAway, "gateway closing")
		cancel()
		return
	}
	old := g.conns[nodeID]
	g.conns[nodeID] = nc
	if old == nil {
		g.connVersion.Add(1)
	}
	if t := g.timers[nodeID]; t != nil {
		t.timer.Stop()
		delete(g.timers, nodeID)
	}
	g.mu.Unlock()
	g.machineAuthMu.Unlock()
	if old != nil {
		old.cancel()
		old.ws.CloseNow()
	}
	g.store.Event("node.connected", nodeID, "", "", r.RemoteAddr)
	g.logLine("node.connected", "node", nodeID, "host", g.store.hostname(nodeID), "remote", r.RemoteAddr)

	health.Received()
	go health.Run(ctx, ws, g.opts.KeepalivePingInterval, g.opts.KeepaliveTimeout)
	g.serve(ctx, nc)

	close(nc.dead)
	cancel()
	g.mu.Lock()
	if g.conns[nodeID] == nc && !g.closed {
		delete(g.conns, nodeID)
		g.connVersion.Add(1)
		t := &offlineTimer{}
		t.timer = time.AfterFunc(g.opts.OfflineGrace, func() { g.nodeGone(nodeID, t) })
		g.timers[nodeID] = t
	}
	g.mu.Unlock()
	ws.CloseNow()
	g.store.Event("node.disconnected", nodeID, "", "", "")
	g.logLine("node.disconnected", "node", nodeID, "host", g.store.hostname(nodeID))
}

func (g *Gateway) nodeGone(nodeID string, timer *offlineTimer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timers[nodeID] != timer {
		return
	}
	_, live := g.conns[nodeID]
	closed := g.closed
	delete(g.timers, nodeID)
	if !live && !closed {
		if err := g.store.SetNodeOffline(nodeID); err != nil {
			g.logLine("error", "where", "node offline", "err", err)
		}
	}
}

func (g *Gateway) serve(ctx context.Context, nc *nodeConn) {
	lane := newFrameLane()
	defer lane.stop()
	for {
		_, raw, err := nc.health.Read(ctx, nc.ws)
		if err != nil {
			return
		}
		var h protocol.Header
		if json.Unmarshal(raw, &h) != nil {
			continue
		}
		if err := g.store.graphFrameAllowed(nc, h.Type); err != nil {
			reply, _ := protocol.EncodeResult(h.ID, nil, err)
			_ = nc.write(ctx, reply)
			continue
		}
		if h.Type == "result" {
			// Results to msg.deliver only say "accepted"; delivery.update has the outcome.
			// Results to the gateway's own calls (node.upgrade) go to their waiter.
			nc.answer(h)
			continue
		}
		if h.Type == "owner.ui.request" || h.Type == "machine.rotate" || h.Type == "machine.remove" || h.Type == "session.sync" || h.Type == "session.upsert" || h.Type == "project.add" || h.Type == "tools.new_projects" || h.Type == "tools.toggle" || h.Type == "cto.proposal.validate" || h.Type == "tools.read_file" || h.Type == "tools.import" || h.Type == "tools.import_folder" || h.Type == "tools.keep_copy" || h.Type == "gateway.backup" || h.Type == "tools.plan" || h.Type == "tools.apply" || h.Type == "tools.undo" || h.Type == "tools.skill_details" || h.Type == "tools.summary" {
			go g.answer(ctx, nc, h, raw)
			continue
		}
		if strings.HasPrefix(h.Type, "mcp.") {
			go g.serveMCP(ctx, nc, h, raw)
			continue
		}
		if isRelay(h.Type) {
			go g.serveRelay(ctx, nc, h, raw)
			continue
		}
		if strings.HasPrefix(h.Type, "worker.") {
			// Starting and stopping wait for another machine, so they must not
			// block this node's other frames.
			go g.serveWorker(ctx, nc, h, raw)
			continue
		}
		if isSessionRelay(h.Type) || h.Type == protocol.FrameProjectChange || h.Type == protocol.FrameSessionTrustFolder || h.Type == protocol.FrameSessionMCPServers || h.Type == protocol.FrameSessionStopProcess {
			// The target machine may ask this gateway a question while it acts,
			// so the relay must not run in this node's read loop.
			go g.answer(ctx, nc, h, raw)
			continue
		}
		if h.Type == "hello" {
			// The only frame the loop runs itself: later frames are checked
			// against what it sets, and the node waits for its answer.
			if g.answer(ctx, nc, h, raw) != nil {
				return
			}
			continue
		}
		if parallelFrames[h.Type] {
			go g.answerOrDrop(ctx, nc, h, raw)
			continue
		}
		lane.submit(func() {
			if h.Type == "msg.send" && g.msgSendMayResume(raw) {
				// Starting an ended worker again asks this same node and waits
				// for its answer. The answer comes to the read loop, which is
				// free, but the lane must not wait for the worker to start.
				go g.answerOrDrop(ctx, nc, h, raw)
				return
			}
			g.answerOrDrop(ctx, nc, h, raw)
		})
	}
}

// answerOrDrop is answer for a frame that does not run in the read loop. When
// the result cannot be written the link is gone, so it ends the connection.
func (g *Gateway) answerOrDrop(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) {
	if g.answer(ctx, nc, h, raw) != nil {
		nc.cancel()
		nc.ws.CloseNow()
	}
}

// msgSendMayResume tells whether a msg.send is addressed to a worker session.
// The worker may end between this check and the handler, which then asks the
// machine to start it again; that ask waits for this same node's answer, so
// the handler must not run in the read loop. Any worker session qualifies:
// online or not, the handler decides whether a resume is needed.
func (g *Gateway) msgSendMayResume(raw []byte) bool {
	var p protocol.MsgSend
	if json.Unmarshal(raw, &p) != nil || p.Recipient == "" {
		return false
	}
	a, ok, err := g.store.workerForContinue(p.Recipient, p.Sender)
	return err == nil && ok && a.Worker != nil
}

// answer handles one frame and writes its result. A link error ends the loop.
func (g *Gateway) answer(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) error {
	start, sent := time.Now(), 0
	defer g.finishRequest(h, nc.id, start, &sent)
	data, err := g.handle(ctx, nc, h.Type, raw)
	var reply []byte
	var eerr error
	if err != nil {
		var rj Rejection
		if !errors.As(err, &rj) {
			g.logLine("error", "node", nc.id, "where", h.Type, "err", err)
		}
		reply, eerr = protocol.EncodeResult(h.ID, nil, err)
	} else {
		reply, eerr = protocol.EncodeResult(h.ID, data, nil)
	}
	if eerr != nil {
		g.logLine("error", "where", "encode result", "err", eerr)
		return nil
	}
	sent = len(reply)
	if nc.write(ctx, reply) != nil {
		return errNodeGone
	}
	if (h.Type == "machine.rotate" || h.Type == "machine.remove") && err == nil {
		if result, ok := data.(map[string]bool); ok && result["reconnect"] {
			nc.ws.Close(websocket.StatusServiceRestart, "machine credentials changed")
		}
	}
	if h.Type == "hello" && err != nil {
		if on, _ := g.store.graphRefsEnabled(); on {
			return errNodeGone
		}
	}
	if h.Type == "session.sync" && err == nil {
		g.sendMCPSync(ctx, nc)
		g.sendSettingsSync(ctx, nc)
		g.dispatchPending(ctx, nc.id, true)
		g.wakePending(ctx, nc.id)
	}
	if (h.Type == "session.upsert" || h.Type == "session.agent.reserve") && err == nil {
		g.dispatchPending(ctx, nc.id, false)
	}
	return nil
}

func decode[T any](raw []byte) (T, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	return v, err
}

func (g *Gateway) handle(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, error) {
	st := g.store
	if err := st.graphFrameAllowed(nc, typ); err != nil {
		return nil, err
	}
	if st.machineRemoved(nc.id) {
		return nil, reject("this machine was removed from the network; run agent-net setup to join again")
	}
	if typ == protocol.FrameTopicsSubscribe {
		return g.subscribeTopics(ctx, nc), nil
	}
	if strings.HasPrefix(typ, "xfer.") {
		return g.handleTransfer(nc, typ, raw)
	}
	if (typ == "session.sync" || typ == "session.upsert" || typ == "project.add") && !g.skillInputsSettled() {
		// Freeze defaults before discovery so existing off cells stay off.
		// Only a skill without its settings has defaults to freeze. When no
		// skill, project or mark changed since the choice last had nothing to
		// do, there is nothing to freeze and nothing to discover.
		missing, err := st.skillSettingsMissing()
		if err != nil {
			return nil, err
		}
		if missing {
			if _, err := st.ToolProjectStates(); err != nil {
				return nil, err
			}
		}
		defer g.scheduleNewSkillProjects()
	}
	switch typ {
	case "owner.ui.request":
		return g.requestOwnerUI(ctx, nc, raw)
	case "owner.worker_floors.reset":
		var p struct {
			Owner string `json:"owner"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		_, err := st.db.Exec(`DELETE FROM owner_settings WHERE key=?`, workerFloorsKey)
		if err == nil {
			st.Event("worker_floors.reset", nc.id, p.Owner, "", "")
		}
		return map[string]bool{"reset": err == nil}, err
	case "owner.invite":
		var p struct {
			Owner string `json:"owner"`
			Host  string `json:"host"`
			Port  string `json:"port"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		port, err := strconv.Atoi(p.Port)
		if err != nil || port < 1 || port > 65535 || p.Host == "" || strings.ContainsAny(p.Host, "'\"\r\n") {
			return nil, reject("invalid invite address")
		}
		return Invite(InviteOptions{Host: p.Host, Port: p.Port, Data: g.opts.DataDir})
	case "cto.session.register", "cto.session.restore":
		p, err := decode[struct {
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if typ == "cto.session.restore" {
			id, err := st.CTOSessionID()
			if err != nil {
				return nil, err
			}
			if id != "" {
				agent, found, err := st.Agent(id)
				if err != nil {
					return nil, err
				}
				if found && agent.NodeID != nc.id {
					return nil, reject("CTO is registered on another machine")
				}
				if found {
					return map[string]string{"session_id": id}, nil
				}
			}
		}
		id, err := st.RegisterCTOSession(nc.id)
		return map[string]string{"session_id": id}, err
	case "cto.proposal.validate":
		return g.validateCTOProposal(ctx, nc, raw)
	case "hello":
		p, err := decode[protocol.Hello](raw)
		if err != nil {
			return nil, reject("bad hello")
		}
		nc.sessionAgents.Store(p.SessionAgents)
		if err := st.SetNodeGraphRefs(nc.id, p.Version, p.GraphRefs); err != nil {
			return nil, err
		}
		var supported bool
		if err := st.db.QueryRow(`SELECT supported FROM node_graph_refs WHERE node_id=?`, nc.id).Scan(&supported); err != nil {
			return nil, err
		}
		nc.graphRefs.Store(supported)
		if err := st.SetNodeHello(nc.id, p.Hostname, p.Version); err != nil {
			return nil, err
		}
		if err := st.SetNodeHealth(nc.id, p.Health); err != nil {
			return nil, err
		}
		// Older daemons send no inventory; their stored one stays as it is.
		if _, err := st.SetNodeHarnesses(nc.id, p.Harnesses); err != nil {
			return nil, err
		}
		if err := st.SetNodeLaunch(nc.id, p.Launch); err != nil {
			return nil, err
		}
		// Older daemons send no time zone; their stored one stays as it is.
		if err := st.SetNodeTimeZone(nc.id, p.TimeZone); err != nil {
			return nil, err
		}
		if err := st.SetNodeTailscaleIP(nc.id, p.TailscaleIP); err != nil {
			return nil, err
		}
		// A completed hello may be the last report the cutover was waiting for.
		// The check runs in the background because this handler can hold the
		// machine authentication lock.
		if on, err := st.graphRefsEnabled(); err == nil && !on {
			g.triggerGraphCutover(context.Background())
		}
		// The daemon uses the name the gateway lists (the owner's display name
		// when there is one) where it compares host names.
		return map[string]string{"node_id": nc.id, "hostname": st.hostname(nc.id)}, nil

	case "project.display.get", "project.display.set":
		p, err := decode[protocol.ProjectDisplayRequest](raw)
		if err != nil || p.Project == "" {
			return nil, reject("project identity required")
		}
		if typ == "project.display.set" {
			return st.SetProjectDisplayName(p.Project, p.Name)
		}
		return st.ProjectDisplayName(p.Project)

	case "machine.remove", "machine.rotate":
		return g.machineAction(ctx, nc, typ, raw)

	case "node.rename.validate":
		p, err := decode[protocol.NodeRename](raw)
		if err != nil || p.NodeID == "" {
			return nil, reject("bad node.rename")
		}
		_, _, err = st.checkNodeDisplayName(p.NodeID, p.Name)
		if err != nil {
			return nil, reject("%s", err.Error())
		}
		return map[string]bool{"ok": true}, nil
	case "node.rename":
		p, err := decode[protocol.NodeRename](raw)
		if err != nil || p.NodeID == "" {
			return nil, reject("bad node.rename")
		}
		res, err := st.SetNodeDisplayName(p.NodeID, p.Name)
		var bad ErrNodeName
		if errors.As(err, &bad) {
			return nil, reject("%s", bad.Reason)
		}
		if err != nil {
			return nil, err
		}
		g.logLine("node.renamed", "by", nc.id, "actor", graphActor(raw), "node", p.NodeID, "name", res.DisplayName)
		if c := g.conn(p.NodeID); c != nil {
			if frame, err := protocol.EncodeRequest("node.name", protocol.NewID("d"), protocol.NodeName{Hostname: res.Hostname}); err == nil {
				c.write(ctx, frame)
			}
		}
		return res, nil

	case "node.inventory":
		p, err := decode[protocol.InventoryReport](raw)
		if err != nil || (p.State != protocol.InventoryOK && p.State != protocol.InventoryError) || len(p.Items) > 10000 {
			return nil, reject("bad node.inventory")
		}
		return nil, st.SetInventory(nc.id, p)

	case "gateway.backup":
		p, err := decode[struct {
			Path string `json:"path"`
		}](raw)
		if err != nil || p.Path == "" {
			return nil, reject("backup needs a path")
		}
		if err = g.Backup(p.Path); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case "tools.toggle":
		p, err := decode[struct {
			protocol.ToolToggleRequest
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !validChangeActor(p.Actor) || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.toggleTool(ctx, p.ToolToggleRequest, p.Actor)
	case "tools.new_projects":
		p, err := decode[struct {
			protocol.ToolNewProjectsRequest
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !validChangeActor(p.Actor) || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.setToolNewProjects(ctx, p.ToolNewProjectsRequest, p.Actor)
	case "query.tools_projects":
		p, err := decode[struct {
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return st.ToolProjectStates()
	case "tools.plan":
		p, err := decode[struct {
			protocol.ToolPlanRequest
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !validChangeActor(p.Actor) || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.planTool(ctx, p.ToolPlanRequest, p.Actor)
	case "tools.skill_details":
		p, err := decode[protocol.ToolSkillScan](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.toolSkillDetails(ctx, p)
	case "tools.report":
		p, err := decode[protocol.ToolResult](raw)
		if err != nil {
			return nil, reject("bad tool result")
		}
		return nil, st.ReportToolResult(nc.id, p)

	case "tools.targets":
		return st.ToolTargets(nc.id)

	case "tools.apply":
		p, err := decode[struct {
			protocol.ToolPlanID
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !validChangeActor(p.Actor) || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.applyTool(ctx, p.ID, p.Actor)
	case "tools.undo":
		p, err := decode[struct {
			protocol.ToolUndoRequest
			Actor string `json:"actor"`
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !validChangeActor(p.Actor) || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if p.Item == "" {
			return nil, reject("undo needs an item")
		}
		return g.undoTool(ctx, p.Item, p.Actor)
	case "query.tools_needs":
		// Retain the old read method for clients predating the removed needs field.
		return []protocol.ToolNeed{}, nil
	case "query.tools_accepted":
		keys, err := st.ToolsAccepted()
		if err != nil {
			return nil, err
		}
		return keys, nil
	case "tools.accept":
		p, err := decode[struct {
			protocol.ToolAccept
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if p.Key == "" || len(p.Key) > 512 {
			return nil, reject("accept needs a key")
		}
		return nil, st.SetToolAccepted(p.Key, p.Accepted)
	case "tools.history":
		p, err := decode[struct {
			protocol.ToolHistoryRequest
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if p.Item == "" {
			return nil, reject("history needs an item")
		}
		events, err := st.ToolHistory(p.Item)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": events}, nil
	case "tools.summary":
		p, err := decode[protocol.SkillSummaryRequest](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		summary, err := st.phraseCleaner.Summarize(ctx, p.AHash, p.BHash, p.A, p.B)
		if err != nil {
			return nil, err
		}
		return map[string]string{"summary": summary}, nil
	case "tools.read_file":
		p, err := decode[protocol.SkillTextRequest](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.readSkillText(ctx, p)
	case "tools.keep_copy":
		p, err := decode[protocol.ToolCopyChoice](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.keepSkillCopy(ctx, p)
	case "tools.import_folder":
		p, err := decode[protocol.ToolFolderImport](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return g.importSkillFolder(ctx, p)
	case "tools.import":
		p, err := decode[protocol.ToolImport](raw)
		if err != nil {
			return nil, reject("invalid skill import")
		}
		return g.importSkill(ctx, p)

	case "tools.versions":
		p, err := decode[protocol.ToolVersionRequest](raw)
		if err != nil || p.Item == "" {
			return nil, reject("versions needs an item")
		}
		return g.skillVersions(p.Item)
	case "tools.export":
		p, err := decode[protocol.ToolVersionRequest](raw)
		if err != nil || p.Item == "" || p.Version == "" {
			return nil, reject("export needs an item and version")
		}
		return g.exportSkill(p.Item, p.Version)

	case "query.inventory":
		p, err := decode[struct {
			Offset int `json:"offset"`
		}](raw)
		if err != nil {
			return nil, reject("bad query.inventory")
		}
		reports, err := st.Inventories()
		if err != nil {
			return nil, err
		}
		for i := range reports {
			if g.conn(reports[i].NodeID) == nil {
				markInventoryOffline(&reports[i])
			}
		}
		return inventoryPage(reports, p.Offset)

	case "query.mcp_import_candidates":
		p, err := decode[protocol.MCPImportCandidatesRequest](raw)
		if err != nil || p.Offset < 0 {
			return nil, reject("bad query.mcp_import_candidates")
		}
		return g.mcpImportCandidates(p)

	case "query.tools_items":
		p, err := decode[protocol.ToolsFilter](raw)
		if err != nil || p.Offset < 0 {
			return nil, reject("bad query.tools_items")
		}
		items, err := st.ToolsItems(p, func(id string) bool { return g.conn(id) != nil })
		if err != nil {
			return nil, err
		}
		return toolsPage(items, p.Offset, g.opts.ToolsPageSize)
	case "query.tools_item":
		p, err := decode[struct {
			Item string `json:"item"`
		}](raw)
		if err != nil || p.Item == "" {
			return nil, reject("bad query.tools_item")
		}
		detail, err := st.ToolsItem(p.Item, func(id string) bool { return g.conn(id) != nil })
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(detail)
		if err != nil {
			return nil, err
		}
		if len(data) > 3<<20 {
			return nil, reject("tool detail exceeds response limit")
		}
		return detail, nil

	case "node.harnesses":
		p, err := decode[protocol.NodeHarnesses](raw)
		if err != nil || p.Harnesses == nil {
			return nil, reject("bad node.harnesses")
		}
		if _, err = st.SetNodeHarnesses(nc.id, p.Harnesses); err != nil {
			return nil, err
		}
		if p.Health != nil {
			if err := st.SetNodeHealth(nc.id, p.Health); err != nil {
				return nil, err
			}
		}
		return nil, st.SetNodeLaunch(nc.id, p.Launch)

	case protocol.FrameSessionStart, protocol.FrameSessionProposalStart, protocol.FrameSessionProposalClone, protocol.FrameSessionResume, protocol.FrameSessionStop, protocol.FrameSessionGet:
		return g.handleSessionRelay(ctx, nc, typ, raw)

	case protocol.FrameSessionTrustFolder:
		return g.handleFolderTrustRelay(ctx, nc, raw)

	case protocol.FrameSessionMCPServers:
		return g.handleFolderMCPRelay(ctx, nc, raw)
	case protocol.FrameSessionStopProcess:
		return g.handleProcessStopRelay(ctx, nc, raw)

	case "query.projects":
		p, err := decode[protocol.QueryProjects](raw)
		if err != nil {
			return nil, reject("bad query.projects")
		}
		ps, err := st.Projects(p.Name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"projects": ps}, nil

	case protocol.FrameProjectChange:
		return g.handleProjectChange(ctx, nc, raw)

	case "project.add":
		p, err := decode[protocol.ProjectAdd](raw)
		if err != nil {
			return nil, reject("bad project.add")
		}
		return st.AddProjects(nc.id, p)

	case "project.remove":
		p, err := decode[protocol.ProjectRemove](raw)
		if err != nil {
			return nil, reject("bad project.remove")
		}
		node := nc.id
		if p.On != "" {
			if node, err = st.NodeByHostOrID(p.On); err != nil {
				return nil, err
			}
		}
		removed, err := st.RemoveProject(node, p.Name, p.Path, nc.id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"removed": removed}, nil

	case "query.nodes":
		return g.NodesInfo()

	case "query.graph_talk":
		return st.GraphTalkSnapshot()

	case "query.graph":
		return g.handleGraphView(raw)
	case "query.team_lead":
		p, err := decode[protocol.QueryTeamLead](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad query.team_lead")
		}
		return st.TeamLeadFor(p.SessionID)
	case "graph.read":
		return g.handleGraphRead(raw)

	case "query.events":
		p, err := decode[protocol.QueryEvents](raw)
		if err != nil {
			return nil, reject("bad query.events")
		}
		evs, err := st.feedEventsBefore(p.AfterID, p.BeforeID, p.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": evs}, nil

	case "cto.presence":
		var p struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		}
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" || (p.Status != "idle" && p.Status != "dormant") {
			return nil, reject("bad CTO presence")
		}
		// Only the host of the registered CTO can report its runtime state.
		_, err := st.db.Exec(`UPDATE sessions SET status=?, last_seen=? WHERE id=? AND node_id=? AND harness='cto' AND id=(SELECT value FROM owner_settings WHERE key='cto_session')`, p.Status, st.now().UnixNano(), p.SessionID, nc.id)
		return struct{}{}, err

	case "session.sync":
		p, err := decode[protocol.SessionSync](raw)
		if err != nil {
			return nil, reject("bad session.sync")
		}
		out, err := st.SyncSessions(nc.id, p.Sessions)
		if err != nil {
			return nil, err
		}
		g.dispatchStepTasks(ctx)
		res := make([]map[string]string, 0, len(out))
		for _, s := range out {
			res = append(res, map[string]string{"native_id": s.NativeID, "harness": s.Harness, "session_id": s.SessionID})
		}
		return map[string]any{"sessions": res}, nil

	case "session.agent.status":
		p, err := decode[protocol.SessionAgentReservation](raw)
		if err != nil {
			return nil, reject("bad session.agent.status")
		}
		return st.SessionAgentStartStatus(nc.id, p.Owner, p.Ref)
	case "session.repo.reserve":
		p, err := decode[protocol.RepoStartReservation](raw)
		if err != nil {
			return nil, reject("bad repo start reservation")
		}
		return st.ReserveRepoStart(nc.id, p)
	case "session.agent.reserve":
		p, err := decode[protocol.SessionAgentReservation](raw)
		if err != nil {
			return nil, reject("bad session.agent.reserve")
		}
		return struct{}{}, st.ReserveSessionAgent(nc.id, p)

	case "session.start.failed":
		p, err := decode[protocol.SessionStartFailure](raw)
		if err != nil {
			return nil, reject("bad session.start.failed")
		}
		if err := st.RecordSessionStartFailure(nc.id, p); err != nil {
			return nil, err
		}
		g.logLine("session.start_failed", "node", nc.id, "host", st.hostname(nc.id), "owner", p.Owner,
			"start", p.StartID, "project", p.Project, "harness", p.Harness, "error", p.Error)
		return struct{}{}, nil

	case "session.upsert":
		p, err := decode[protocol.SessionUpsert](raw)
		if err != nil {
			return nil, reject("bad session.upsert")
		}
		id, err := st.UpsertSession(nc.id, p.Session)
		if err != nil {
			return nil, err
		}
		g.dispatchStepTasks(ctx)
		return map[string]string{"session_id": id}, nil

	case "session.remove":
		p, err := decode[protocol.SessionRemove](raw)
		if err != nil {
			return nil, reject("bad session.remove")
		}
		if p.Closed || p.Reopen {
			return nil, st.SetSessionClosed(nc.id, p.SessionID, p.Closed)
		}
		if p.Lost {
			return nil, st.SetSessionLost(nc.id, p.SessionID)
		}
		return nil, st.SetSessionOffline(nc.id, p.SessionID)

	case "session.archive":
		p, err := decode[protocol.SessionRemove](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad session.archive")
		}
		return nil, st.CloseArchivedSessionInbox(nc.id, p.SessionID)

	case "session.agent.list":
		var p struct {
			Owner string `json:"owner_session"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad session.agent.list")
		}
		return st.sessionAgentOptions(nc.id, p.Owner)

	case "session.agent.set":
		p, err := decode[protocol.SessionAgentSetRequest](raw)
		if err != nil {
			return nil, reject("bad session.agent.set")
		}
		if err = st.setSessionAgent(nc.id, p); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case "session.pin":
		var p struct {
			Owner     string `json:"owner"`
			SessionID string `json:"session_id"`
			Pinned    bool   `json:"pinned"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if p.SessionID == "" {
			return nil, reject("session_id is required")
		}
		if err := st.SetSessionPin(p.SessionID, p.Pinned); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case "session.owner_use":
		var p struct {
			Owner     string `json:"owner"`
			SessionID string `json:"session_id"`
			Prompt    bool   `json:"prompt"`
		}
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			return nil, reject("bad session.owner_use")
		}
		if p.Prompt {
			a, ok, err := st.Agent(p.SessionID)
			if err != nil {
				return nil, err
			}
			if !ok || a.NodeID != nc.id || (a.Harness != "claude" && a.Harness != "pi" && a.Harness != "codex") {
				return nil, reject("session does not belong to this node")
			}
		} else if !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if err := st.RecordOwnerUse(p.SessionID); err != nil {
			return nil, err
		}
		if p.Prompt {
			// The owner typed in the session: what it asked is answered or
			// overtaken (the Needs you tag must not outlive its reason).
			if _, err := st.closeSessionOwnerAsks(st.db, p.SessionID, 0, protocol.AskReasonOwnerReplied); err != nil {
				return nil, err
			}
		}
		return map[string]bool{"ok": true}, nil

	case "sessions.archive", "sessions.archived":
		var p struct {
			protocol.SessionsArchiveRequest
			Owner string `json:"owner"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		if typ == "sessions.archived" {
			return st.ArchivedSessions()
		}
		return st.ArchiveSessions(p.SessionsArchiveRequest)

	case "session.rename":
		p, err := decode[protocol.SessionRenameRequest](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad session.rename")
		}
		res, err := st.RenameSession(p.SessionID, p.Name)
		var bad ErrSessionName
		if errors.As(err, &bad) {
			return nil, reject("%s", bad.Reason)
		}
		if err != nil {
			return nil, err
		}
		g.logLine("session.renamed", "by", nc.id, "session", p.SessionID, "name", res.Name)
		return res, nil

	case "session.trust":
		p, err := decode[protocol.SessionTrust](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad session.trust")
		}
		ok, err := st.setPeerTrust(p.SessionID, p.On, graphActor(raw))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown or untrustable session %q", p.SessionID)
		}
		st.Event("session.trust", nc.id, p.SessionID, "", "on="+strconv.FormatBool(p.On))
		g.logLine("session.trust", "node", nc.id, "session", p.SessionID, "on", p.On)
		return map[string]any{"session_id": p.SessionID, "on": p.On}, nil

	case "learn.add":
		p, err := decode[protocol.LearnFrame](raw)
		if err != nil || p.Reporter == "" {
			return nil, reject("bad learn.add")
		}
		owner, err := st.SessionNode(p.Reporter)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("session %q does not belong to this node", p.Reporter)
		}
		return st.AddFinding(protocol.Finding{SubjectKind: p.SubjectKind, Subject: p.Subject,
			Finding: p.Finding, Evidence: p.Evidence, Reporter: p.Reporter, NodeID: nc.id})

	case "charter.draft":
		p, err := decode[protocol.CharterDraftFrame](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad charter.draft")
		}
		owner, err := st.SessionNode(p.SessionID)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("session %q does not belong to this node", p.SessionID)
		}
		res, err := st.SubmitCharterDraft(p.SessionID, p.CharterDraftRequest, p.SourceHash)
		g.flushLeadRequests(ctx)
		return res, err

	case "rate.offer":
		p, err := decode[protocol.RateOfferFrame](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad rate.offer")
		}
		owner, err := st.SessionNode(p.SessionID)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("session %q does not belong to this node", p.SessionID)
		}
		return st.OfferRate(p.SessionID)

	case "rate.add":
		p, err := decode[protocol.RateFrame](raw)
		if err != nil || p.Rater == "" {
			return nil, reject("bad rate.add")
		}
		owner, err := st.SessionNode(p.Rater)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("session %q does not belong to this node", p.Rater)
		}
		return st.AddRating(p)

	case "cto.graph_latest":
		id, err := st.CTOLastGraphID()
		return map[string]int64{"id": id}, err
	case "cto.graph_history":
		var p struct {
			After int64 `json:"after"`
		}
		if json.Unmarshal(raw, &p) != nil || p.After < 0 {
			return nil, reject("bad CTO history")
		}
		return st.CTOGraphHistory(p.After)
	case "held.list":
		messages, err := st.HeldRequests()
		return map[string]any{"messages": messages}, err
	case "held.answer":
		p, err := decode[protocol.HeldAnswer](raw)
		if err != nil {
			return nil, reject("bad held.answer")
		}
		m, err := st.answerHeld(p.MessageID, p.Action, graphActor(raw))
		if err != nil {
			return nil, err
		}
		if m.OwnerApproved {
			g.dispatch(ctx, m)
		} else {
			if a, ok, err := st.Agent(m.From); err == nil && ok && a.Online {
				g.dispatchPending(ctx, a.NodeID, false)
			}
		}
		return map[string]any{"message": m}, nil

	case "cto.graph_class":
		var q struct {
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		}
		if json.Unmarshal(raw, &q) != nil {
			return nil, reject("bad graph classification")
		}
		return st.CTOGraphClass(q.Tool, q.Args)
	case "cto.capabilities":
		return map[string]bool{"attribution": true, "network_session": true, "graph_authority": true}, nil
	case "team.write", "team.history":
		q, err := decode[protocol.TeamWrite](raw)
		if err != nil || !g.validToolOwner(nc, q.OwnerSession) {
			return nil, reject("owner authority required")
		}
		if typ == "team.history" {
			return st.TeamHistory(q.ID, 1000)
		}
		return map[string]bool{"ok": true}, st.WriteTeam(q, graphActor(raw))
	case "graph.get":
		p, err := decode[protocol.QueryGraph](raw)
		if err != nil {
			return nil, reject("bad graph.get")
		}
		if p.Typed || p.Ref != nil {
			return st.GraphForRef(p.Ref)
		}
		return st.visibleGraph(p.All)
	case "standing.list", "standing.save", "standing.remove", "standing.update", "standing.search", "standing.history":
		q, err := decode[protocol.StandingOrdersRequest](raw)
		if err != nil {
			return nil, reject("bad %s", typ)
		}
		res := protocol.StandingOrdersResult{Orders: []protocol.StandingOrder{}}
		switch typ {
		case "standing.search":
			res.Orders, err = st.SearchStandingOrders(q)
			return res, err
		case "standing.history":
			res.History, err = st.StandingOrderHistory(q.ID)
			return res, err
		case "standing.update":
			o, e := st.UpdateStandingOrder(q)
			if e != nil {
				return nil, e
			}
			res.Order = &o
		case "standing.save":
			o, err := st.SaveStandingOrder(q)
			if err != nil {
				return nil, reject("%v", err)
			}
			res.Order = &o
		case "standing.remove":
			o, err := st.RemoveStandingOrder(q.ID)
			if err != nil {
				return nil, reject("%v", err)
			}
			res.Order = o
		}
		if res.Orders, err = st.StandingOrders(); err != nil {
			return nil, err
		}
		return res, nil
	case "judgment.find":
		q, err := decode[protocol.JudgmentsFindRequest](raw)
		if err != nil {
			return nil, reject("bad judgment.find")
		}
		items, err := st.FindJudgments(q)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items, "total": len(items)}, nil
	case "judgment.add":
		q, err := decode[protocol.JudgmentAddRequest](raw)
		if err != nil {
			return nil, reject("bad judgment.add")
		}
		return st.AddJudgment(q)
	case "playbook.set":
		p, err := decode[protocol.PlaybookSetRequest](raw)
		if err != nil {
			return nil, reject("bad playbook.set")
		}
		model, harness, note, err := protocol.CleanPlaybook(p.Model, p.Harness, p.Note)
		if err != nil {
			return nil, reject("%v", err)
		}
		if err := st.SetPlaybookPin(model, harness, note, p.Pin); err != nil {
			return nil, err
		}
		if note == "" {
			return protocol.PlaybookGetResult{}, nil
		}
		kind := protocol.SubjectModel
		if harness != "" {
			kind = protocol.SubjectModelHarness
		}
		pinned := p.Pin != nil && *p.Pin
		return protocol.PlaybookGetResult{Note: &protocol.PlaybookNote{SubjectKind: kind, Subject: model, Harness: harness,
			Note: note, Source: protocol.PlaybookSourceOwner, Pinned: pinned}}, nil
	case "playbook.get":
		p, err := decode[protocol.PlaybookGetRequest](raw)
		if err != nil {
			return nil, reject("bad playbook.get")
		}
		note, _, err := st.PlaybookFor(p.Model, p.Harness)
		if err != nil {
			return nil, err
		}
		return protocol.PlaybookGetResult{Note: note}, nil
	case "charter.validate":
		p, err := decode[protocol.CharterSetRequest](raw)
		if err != nil {
			return nil, reject("bad charter.set")
		}
		return map[string]bool{"ok": true}, st.SetLevelCharter(p, "owner", true)
	case "charter.set":
		p, err := decode[protocol.CharterSetRequest](raw)
		if err != nil {
			return nil, reject("bad charter.set")
		}
		if err := st.SetLevelCharter(p, graphActor(raw), false); err != nil {
			return nil, err
		}
		g.dispatchStepTasks(ctx)
		result := protocol.CharterSetResult{OK: true}
		if graph, err := st.Graph(); err == nil {
			for _, node := range graph.Nodes {
				if node.Name == p.Node {
					result.Warnings = protocol.CharterWarnings(node.Charter)
					break
				}
			}
		}
		return result, nil
	case "charter.history":
		p, err := decode[protocol.GraphHistoryRequest](raw)
		if err != nil || (p.Node == "" && p.Ref == nil) {
			return nil, reject("bad charter.history")
		}
		if p.Ref != nil {
			return st.RefCharterHistory(*p.Ref, p.Limit)
		}
		return st.CharterHistory(p.Node, p.Limit)
	case "lead.set":
		p, err := decode[protocol.LeadSetRequest](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		err = st.SetLead(p, graphActor(raw), false)
		return map[string]bool{"ok": err == nil}, err
	case "repo.create", "repo.archive", "agent.create", "agent.tombstone":
		if err := st.requireGraphRefs(); err != nil {
			return nil, err
		}
		p, err := decode[protocol.GraphLifecycleRequest](raw)
		if err != nil {
			return nil, reject("bad lifecycle request")
		}
		if !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		err = st.GraphLifecycle(typ, p, graphActor(raw), false)
		return map[string]any{"ok": err == nil, "retained": "Charters, settings, offline sessions and run and delivery history remain attached; an archived repo keeps its owner. Active edges and assignments must first be removed."}, err
	case "node.add", "node.remove":
		p, err := decode[protocol.GraphNodeRequest](raw)
		if err != nil || p.Name == "" {
			return nil, reject("bad node request")
		}
		if typ == "node.add" {
			err = st.AddArchitect(p.Name, graphActor(raw))
		} else {
			err = st.RemoveArchitect(p.Name, graphActor(raw))
		}
		return map[string]bool{"ok": true}, err
	case "edge.validate":
		p, err := decode[protocol.Edge](raw)
		if err != nil {
			return nil, reject("bad edge request")
		}
		if p.FromRef != nil || p.ToRef != nil {
			if err := st.requireGraphRefs(); err != nil {
				return nil, err
			}
			if err := validateEdgeRefs(p); err != nil {
				return nil, err
			}
		}
		tx, err := st.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := setEdgeTx(tx, p, "owner", st.now().UnixNano()); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case "edge.set", "edge.remove":
		p, err := decode[protocol.Edge](raw)
		if err != nil {
			return nil, reject("bad edge request")
		}
		if p.FromRef != nil || p.ToRef != nil {
			if err := st.requireGraphRefs(); err != nil {
				return nil, err
			}
			if err := validateEdgeRefs(p); err != nil {
				return nil, err
			}
		}
		if typ == "edge.set" {
			err = st.SetEdge(p, graphActor(raw))
		} else {
			err = st.RemoveEdge(p, graphActor(raw))
		}
		if err == nil {
			g.dispatchStepTasks(ctx)
		}
		return map[string]bool{"ok": true}, err
	case "merger.restore":
		var p struct {
			Owner       string                      `json:"owner_session"`
			Project     string                      `json:"project"`
			Assignments []protocol.MergerSetRequest `json:"assignments"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		err := st.RestoreMergerRegistrations(p.Project, p.Assignments)
		if err == nil {
			g.logLine("merger.restore", "project", p.Project, "by", graphActor(raw))
			g.dispatchStepTasks(ctx)
		}
		return map[string]bool{"ok": err == nil}, err
	case "merger.get":
		var p protocol.MergerSetRequest
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) || p.Project == "" {
			return nil, reject("owner and project required")
		}
		rows, err := st.db.Query(`SELECT project,node_id,session_id FROM project_mergers WHERE project=? ORDER BY node_id`, p.Project)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []protocol.MergerSetRequest{}
		for rows.Next() {
			var q protocol.MergerSetRequest
			if err := rows.Scan(&q.Project, &q.NodeID, &q.SessionID); err != nil {
				return nil, err
			}
			out = append(out, q)
		}
		return out, rows.Err()
	case "merger.plan":
		p, err := decode[protocol.MergerSetRequest](raw)
		if err != nil || p.Project == "" {
			return nil, reject("merger needs project")
		}
		needs, err := st.MergerNeedsDefault(p.Project)
		return map[string]bool{"needs_default": needs}, err
	case "merger.set":
		p, err := decode[protocol.MergerSetRequest](raw)
		if err != nil {
			return nil, reject("bad merger request")
		}
		if p.NodeID != nc.id {
			owner, ok, err := st.Agent(p.Owner)
			if err != nil {
				return nil, err
			}
			if !ok || owner.NodeID != nc.id || !protocol.IsOwner(owner.Harness) || owner.Worker != nil {
				return nil, reject("only the session's machine or an authenticated owner relay may change a merger")
			}
		}
		err = st.SetMerger(p, graphActor(raw))
		if err == nil {
			g.dispatchStepTasks(ctx)
		}
		return map[string]bool{"ok": err == nil}, err
	case "delivery.owner.set":
		p, err := decode[protocol.DeliveryOwnerSetRequest](raw)
		if err != nil {
			return nil, reject("bad delivery owner request")
		}
		err = st.SetDeliveryOwner(p, graphActor(raw))
		if err == nil {
			g.dispatchStepTasks(ctx)
		}
		return map[string]bool{"ok": true}, err
	case "edge.history":
		p, err := decode[protocol.GraphHistoryRequest](raw)
		if err != nil {
			return nil, reject("bad edge.history")
		}
		if p.Ref != nil {
			return st.RefEdgeHistory(*p.Ref, p.Limit)
		}
		return st.EdgeHistory(p.Node, p.Limit)
	case "graph.undo":
		p, err := decode[protocol.GraphUndoFrame](raw)
		if err != nil || p.HistoryID < 0 {
			return nil, reject("bad graph.undo")
		}
		err = st.GraphUndo(p.HistoryID, graphActor(raw))
		if err == nil {
			g.dispatchStepTasks(ctx)
		}
		return map[string]bool{"ok": true}, err
	case "settings.snapshot":
		return st.SettingsSnapshot()
	case "settings.target":
		p, err := decode[protocol.SettingsTarget](raw)
		if err != nil {
			return nil, reject("bad settings.target")
		}
		return st.ResolveSettingsTarget(p)
	case "settings.write":
		p, err := decode[protocol.SettingsWrite](raw)
		if err != nil || len(p.Changes) == 0 {
			return nil, reject("bad settings.write")
		}
		if !settingsActor(p.By) {
			return nil, reject("invalid settings actor")
		}
		result, err := st.WriteSettings(p.Changes, p.By, p.Reason)
		if err == nil {
			g.dispatchStepTasks(ctx)
		}
		return result, err
	case "settings.validate":
		p, err := decode[protocol.SettingsWrite](raw)
		if err != nil || len(p.Changes) == 0 || !settingsActor(p.By) {
			return nil, reject("bad settings.validate")
		}
		return nil, st.ValidateSettings(p.Changes, p.By)
	case "settings.undo":
		p, err := decode[protocol.SettingsUndo](raw)
		if err != nil || p.Revision <= 0 {
			return nil, reject("bad settings.undo")
		}
		if !settingsActor(p.By) {
			return nil, reject("invalid settings actor")
		}
		result, err := st.UndoSettings(p.Revision, p.By)
		if err == nil {
			g.dispatchStepTasks(ctx)
		}
		return result, err
	case "settings.history":
		p, err := decode[struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
			Owner    bool   `json:"owner"`
		}](raw)
		if err != nil || p.Revision < 0 {
			return nil, reject("bad settings.history")
		}
		return st.SettingsHistory(p.Key, p.Revision, p.Owner)
	case "node.settings.get":
		p, err := decode[protocol.NodeSettingsGetRequest](raw)
		if err != nil || p.Node == "" {
			return nil, reject("bad node.settings.get")
		}
		return st.NodeSettings(p.Node)
	case "node.settings.set":
		p, err := decode[protocol.NodeSettingsSetRequest](raw)
		if err != nil || p.Node == "" || p.Setting == "" {
			return nil, reject("bad node.settings.set")
		}
		actor := graphActor(raw)
		if _, moved := deliveryNodeKeys[p.Setting]; moved {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, reject("bad node.settings.set")
			}
			// Old daemons omitted by. If it is present, it must be a known
			// settings writer, never an implicit owner or the cutover actor.
			if supplied, ok := fields["by"]; ok {
				actor = ""
				if err := json.Unmarshal(supplied, &actor); err != nil || !settingsActor(actor) {
					return nil, reject("invalid settings actor")
				}
			}
		}
		if err := st.SetNodeSetting(p.Node, p.Setting, p.Value, actor); err != nil {
			return nil, err
		}
		g.dispatchStepTasks(ctx)
		return st.NodeSettings(p.Node)
	case "onboarding.get":
		return st.Onboarding()
	case "onboarding.set":
		p, err := decode[protocol.OnboardingUpdate](raw)
		if err != nil {
			return nil, reject("bad onboarding.set")
		}
		return st.UpdateOnboarding(p, false)
	case "onboarding.restart":
		return st.UpdateOnboarding(protocol.OnboardingUpdate{}, true)
	case "hidden.get":
		return st.HiddenItems()
	case "hidden.set":
		p, err := decode[protocol.HiddenItems](raw)
		if err != nil {
			return nil, reject("bad hidden.set")
		}
		items, err := st.SetHiddenItems(p)
		if err != nil {
			return nil, reject("%v", err)
		}
		st.Event("hidden.set", nc.id, "", "", fmt.Sprintf("sessions=%d nodes=%d", len(items.Sessions), len(items.Nodes)))
		g.logLine("hidden.set", "node", nc.id, "sessions", len(items.Sessions), "nodes", len(items.Nodes))
		return items, nil

	case "session.policy":
		p, err := decode[protocol.SessionPolicy](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad session.policy")
		}
		clean, err := p.Policy.Clean()
		if err != nil {
			return nil, reject("%v", err)
		}
		if p.Scope == "" {
			p.Scope = protocol.ScopeSession
		}
		if p.Scope != protocol.ScopeSession && p.Scope != protocol.ScopeProject {
			return nil, reject("scope must be %s or %s", protocol.ScopeSession, protocol.ScopeProject)
		}
		if clean.Workers != "" && p.Scope != protocol.ScopeProject {
			return nil, reject("the workers level is set per project: use scope project")
		}
		view, ok, err := st.PolicyDryRun(p.SessionID, p.Scope, clean)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown or unsuitable session %q", p.SessionID)
		}
		// A model name that does not resolve is refused, so the owner learns
		// at once and not on the next turn of the session.
		if view != nil && !p.DryRun {
			if view.CoderError != "" {
				return nil, reject("coder model: %s", view.CoderError)
			}
			if view.MainError != "" {
				return nil, reject("main model: %s", view.MainError)
			}
		}
		res := protocol.SessionPolicyResult{SessionID: p.SessionID, Effective: view}
		if validatingProposal(ctx) {
			return nil, nil
		}
		if !p.DryRun {
			ok, err := st.SetDelegation(p.SessionID, p.Scope, clean)
			if err != nil {
				return nil, reject("%v", err)
			}
			if !ok {
				return nil, reject("unknown or unsuitable session %q", p.SessionID)
			}
			res.Saved = true
			detail := "scope=" + p.Scope + " delegate=" + clean.Delegate + " coder=" + clean.CoderModel + " main=" + clean.MainModel + " workers=" + clean.Workers
			st.Event("session.policy", nc.id, p.SessionID, "", detail)
			g.logLine("session.policy", "node", nc.id, "session", p.SessionID, "scope", p.Scope,
				"delegate", clean.Delegate, "coder", clean.CoderModel, "main", clean.MainModel, "workers", clean.Workers)
		}
		return res, nil

	case "signal.prompt":
		var p struct {
			SessionID string `json:"session_id"`
			Prompt    string `json:"prompt"`
		}
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			return nil, reject("bad signal.prompt")
		}
		a, ok, err := st.Agent(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !ok || a.NodeID != nc.id || a.Harness != "claude" {
			return nil, reject("session does not belong to this node")
		}
		return nil, st.RememberOwnerPrompt(p.SessionID, p.Prompt)

	case "signal.list":
		var p struct {
			Owner string `json:"owner"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return st.Signals()

	case "learned.list", "learned.answer", "learned.view":
		var p struct {
			Owner  string `json:"owner"`
			ID     int64  `json:"suggestion_id"`
			Node   string `json:"node"`
			Action string `json:"action"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		switch typ {
		case "learned.list":
			return st.OwnerLearned()
		case "learned.answer":
			if err := st.DecideLearned(p.ID, p.Action); err != nil {
				return map[string]bool{"ok": true}, err
			}
			err := st.reconcileLearnedAsks()
			g.flushLeadRequests(ctx)
			return map[string]bool{"ok": true}, err
		default:
			return map[string]bool{"ok": true}, st.MarkLearnedViewed(p.Node, p.ID)
		}

	case "context.get":
		p, err := decode[protocol.PolicyGet](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad context.get")
		}
		a, ok, err := st.Agent(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !ok || a.NodeID != nc.id {
			return nil, reject("session %q does not belong to this node", p.SessionID)
		}
		text, err := st.contextText(p.SessionID)
		if err != nil {
			return nil, err
		}
		return protocol.PolicyGot{Charter: text}, nil

	case "charter.global":
		// The owner's global level, for a connection that has no session yet.
		// Charters and rules are public, so any node may read it.
		global, err := st.GlobalCharter()
		if err != nil {
			return nil, err
		}
		return protocol.GlobalCharterGot{Global: global}, nil

	case "policy.get":
		p, err := decode[protocol.PolicyGet](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad policy.get")
		}
		a, ok, err := st.Agent(p.SessionID)
		if err != nil {
			return nil, err
		}
		// A daemon asks only for sessions of its own machine.
		if !ok || a.NodeID != nc.id {
			return nil, reject("session %q does not belong to this node", p.SessionID)
		}
		c, err := st.resolveSessionContext(p.SessionID)
		if err != nil {
			return nil, err
		}
		got := protocol.PolicyGot{Harness: a.Harness, Delegation: a.Delegation, Project: c.Project, Charter: c.agentText()}
		if p.Turn {
			// The one-time charter draft request rides on the per-turn text
			// (decision 0088). A failure here never blocks the turn.
			if ask, derr := st.charterDraftAskFor(p.SessionID, c, p.AgentsHash); derr == nil {
				got.DraftRequest = ask
			}
		}
		return got, nil

	case "profile.announce":
		p, err := decode[protocol.ProfileAnnounce](raw)
		if err != nil {
			return nil, reject("bad profile.announce")
		}
		prof, notice, err := st.AnnounceNotify(nc.id, p)
		if err != nil {
			return nil, err
		}
		// A blocked or waiting announce tells the team lead (decision 0111).
		// The lead may be on another node.
		if notice != nil {
			g.dispatch(ctx, *notice)
		}
		// An announce can answer an idle check with "more", which stores one
		// autonomy wake. Queue it on this node now.
		g.dispatchPending(ctx, nc.id, false)
		return map[string]any{"profile": prof}, nil

	case "inbox.create":
		var p struct {
			SessionID      string   `json:"session_id"`
			Kind           string   `json:"kind"`
			Text           string   `json:"text"`
			Recommendation string   `json:"recommendation"`
			Options        []string `json:"options"`
			Replaces       string   `json:"replaces"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad inbox.create")
		}
		if p.Kind == "check" {
			nodeName := st.hostname(nc.id)
			if nodeName == "" {
				nodeName = nc.id
			}
			sid := p.SessionID
			if sid == "" {
				sid = nc.id
			}
			if cto, _ := st.CTOSessionID(); cto != "" && sid == cto {
				ownerNode, err := st.SessionNode(cto)
				if err != nil || ownerNode != nc.id {
					return nil, reject("CTO session does not belong to this node")
				}
				nodeName = "cto"
			}
			// What the CTO applied or sent is an event, not a question (decision
			// 0090). It is a line of the log with the way to undo it.
			if nodeName == "cto" {
				err := st.deskLog(st.db, "cto", "", "", p.Text, p.Recommendation, p.Options)
				return map[string]any{"item": protocol.InboxItem{Kind: "check", SessionID: sid, Node: nodeName, Text: p.Text, Status: "logged"}}, err
			}
			from := ""
			if strings.HasPrefix(p.Text, "Disk space low on ") {
				from = protocol.AskFromDisk
			}
			item, err := st.CreateInboxItem(protocol.InboxItem{
				Kind:           "check",
				SessionID:      sid,
				Node:           nodeName,
				Text:           p.Text,
				Recommendation: p.Recommendation,
				Options:        p.Options,
				AskerName:      nodeName,
				FromKind:       from,
			})
			return map[string]any{"item": item}, err
		}
		if p.Kind != "approval" && p.Kind != "feature_idea" && p.Kind != "question" {
			return nil, reject("invalid inbox kind")
		}
		agent, project, name, err := st.askingSession(nc.id, p.SessionID)
		if err != nil {
			return nil, err
		}
		ask := protocol.InboxItem{Kind: p.Kind, SessionID: agent.SessionID, Node: project, Text: p.Text, Recommendation: p.Recommendation, Options: p.Options, AskerName: name, Replaces: p.Replaces}
		// A team that sends questions to its lead keeps this one from the owner.
		redirect, m, err := st.RedirectOwnerAsk(ask)
		if err != nil {
			return nil, err
		}
		if redirect != nil {
			if m.State != protocol.StateHeld {
				g.dispatch(ctx, m)
			}
			return map[string]any{"item": protocol.InboxItem{ID: redirect.ItemID, Kind: p.Kind, Status: "open", SessionID: agent.SessionID}, "redirected": redirect}, nil
		}
		item, err := st.CreateInboxItem(ask)
		return map[string]any{"item": item}, err

	case "apps.ask":
		var p struct {
			Owner string `json:"owner"`
			Code  string `json:"code"`
			Kind  string `json:"kind"`
			Name  string `json:"name"`
		}
		if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
		}
		return st.OpenAppConsent(p.Code, p.Kind, p.Name)
	case "ask.redirect":
		// The explicit wait of a plugin asks through the daemon's waiting
		// method. The daemon asks here first whether the team lead takes it.
		var p struct {
			SessionID      string   `json:"session_id"`
			Kind           string   `json:"kind"`
			Text           string   `json:"text"`
			Recommendation string   `json:"recommendation"`
			Options        []string `json:"options"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad ask.redirect")
		}
		if p.Kind != "approval" && p.Kind != "feature_idea" && p.Kind != "question" {
			return nil, reject("invalid inbox kind")
		}
		agent, project, name, err := st.askingSession(nc.id, p.SessionID)
		if err != nil {
			return nil, err
		}
		redirect, m, err := st.RedirectOwnerAsk(protocol.InboxItem{Kind: p.Kind, SessionID: agent.SessionID, Node: project, Text: p.Text, Recommendation: p.Recommendation, Options: p.Options, AskerName: name})
		if err != nil {
			return nil, err
		}
		if redirect == nil {
			return map[string]any{}, nil
		}
		if m.State != protocol.StateHeld {
			g.dispatch(ctx, m)
		}
		return map[string]any{"redirected": redirect}, nil

	case "apps.public.status", "apps.public.on", "apps.public.off", "apps.public.url", "apps.public.restore":
		return g.publicOperation(ctx, typ, raw)
	case "apps.list":
		apps, err := st.ListApps()
		return map[string]any{"apps": apps}, err

	case "apps.pending":
		var p struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad apps.pending")
		}
		now := st.now()
		pending, err := st.PendingByUserCode(p.Code, "tui", now)
		if errors.Is(err, ErrOAuthTooManyTries) {
			var oldest int64
			if e := st.db.QueryRow("SELECT MIN(attempted_at) FROM oauth_code_misses WHERE source=? AND attempted_at>?", "tui", now.Add(-10*time.Minute).UnixNano()).Scan(&oldest); e == nil {
				remaining := time.Unix(0, oldest).Add(10 * time.Minute).Sub(now)
				minutes := max(1, int((remaining+time.Minute-1)/time.Minute))
				err = fmt.Errorf("%w: %d minutes", err, minutes)
			}
		}
		return map[string]any{"pending": pending}, err

	case "apps.approve":
		var p struct {
			ID   string `json:"app_id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad apps.approve")
		}
		var ownerSession string
		if err := st.db.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? LIMIT 1`, nc.id, protocol.OwnerHarness).Scan(&ownerSession); err != nil {
			return nil, fmt.Errorf("owner session required for app approval: %w", err)
		}
		if err := st.ApprovePending(p.ID, p.Kind, p.Name, st.now(), ownerSession); err != nil {
			return nil, err
		}
		var text string
		if err := st.db.QueryRow(`SELECT text FROM desk_log WHERE detail=?`, "app-approval:"+p.ID).Scan(&text); err == nil && g.logger != nil {
			g.logLine("apps.approved", "notice", text)
		}
		return nil, nil

	case "apps.refuse", "apps.revoke":
		var p struct {
			ID string `json:"app_id"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad apps request")
		}
		if typ == "apps.refuse" {
			return nil, st.RefusePending(p.ID)
		}
		return nil, g.revokeApp(ctx, p.ID)

	case protocol.OpAppsRename:
		var p struct {
			ID   string `json:"app_id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID == "" {
			return nil, reject("bad apps.rename")
		}
		return nil, st.RenameApp(p.ID, p.Name)

	case protocol.OpAppsGrant, protocol.OpAppsUngrant:
		var p struct {
			ID    string `json:"app_id"`
			Grant string `json:"grant"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID == "" || p.Grant == "" {
			return nil, reject("bad %s", typ)
		}
		if err := st.SetAppGrant(p.ID, p.Grant, typ == protocol.OpAppsGrant); err != nil {
			return nil, err
		}
		g.logLine(typ, "app", p.ID, "grant", safeAppLog(p.Grant))
		return nil, nil

	case "inbox.list":
		items, err := st.ListInbox()
		return map[string]any{"items": items}, err

	case "asks.list":
		asks, err := st.ListAsks()
		return protocol.AsksResult{Asks: asks}, err

	case "desk.log":
		var p protocol.DeskLogQuery
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad desk.log")
		}
		entries, err := st.DeskLog(p.Limit)
		return protocol.DeskLogResult{Entries: entries}, err

	case "desk.settle":
		var p struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID <= 0 {
			return nil, reject("bad desk.settle")
		}
		return map[string]bool{"ok": true}, st.SettleDeskLog(p.ID)

	case "ask.open":
		var p protocol.AskOpen
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad ask.open")
		}
		ask, err := st.OpenExternalAsk(nc.id, p)
		return map[string]any{"ask": ask}, err

	case "ask.sync":
		var p protocol.AskSync
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad ask.sync")
		}
		return map[string]bool{"ok": true}, st.SyncExternalAsks(nc.id, p)

	case "ask.close":
		var p protocol.AskClose
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad ask.close")
		}
		return map[string]bool{"ok": true}, st.CloseExternalAsk(nc.id, p)

	case "chief.get":
		return st.ChiefStatus()

	case "chief.set":
		p, err := decode[protocol.ChiefSetRequest](raw)
		if err != nil {
			return nil, reject("bad chief.set")
		}
		return map[string]bool{"ok": true}, st.SetChief(p, graphActor(raw))

	case "ask.history":
		p, err := decode[protocol.AskHistoryRequest](raw)
		if err != nil {
			return nil, reject("bad ask.history")
		}
		sorts, err := st.AskSortHistory(p.Ask)
		return protocol.AskHistoryResult{Sorts: sorts}, err

	case "ask.undo":
		p, err := decode[protocol.AskUndoRequest](raw)
		if err != nil {
			return nil, reject("bad ask.undo")
		}
		return st.UndoAsk(p.Ask, graphActor(raw))

	case "ask.mark":
		p, err := decode[protocol.AskMarkRequest](raw)
		if err != nil {
			return nil, reject("bad ask.mark")
		}
		return map[string]bool{"ok": true}, st.MarkAsk(p, graphActor(raw))

	case "chief.sorts":
		sorts, err := st.ChiefSorts(st.now().Add(-protocol.ChiefSortsWindow))
		return protocol.ChiefSortsResult{Sorts: sorts}, err

	case "inbox.answer":
		var p struct {
			ID      string `json:"item_id"`
			Answer  string `json:"answer"`
			FromCTO bool   `json:"from_cto"`
			Actor   string `json:"actor"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID == "" {
			g.logLine("inbox.answer.refused", "id", p.ID, "reason", "bad inbox.answer")
			return nil, reject("bad inbox.answer")
		}
		var owner string
		if err := st.db.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? LIMIT 1`, nc.id, protocol.OwnerHarness).Scan(&owner); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if handled, err := st.answerAppConsent(p.ID, p.Answer, owner, p.FromCTO); handled {
			return map[string]bool{"answered": err == nil}, err
		}
		item, err := st.AnswerInboxAs(p.ID, p.Answer, p.Actor)
		if err != nil {
			g.logLine("inbox.answer.refused", "id", p.ID, "reason", err.Error())
			return nil, err
		}
		g.dispatchStepTasks(ctx)
		node, err := st.SessionNode(item.SessionID)
		if err == nil {
			g.dispatchPending(ctx, node, false)
			// A dismissal sends no message, so the session would keep its
			// wait. Tell its machine.
			if item.Status == "dismissed" && (item.Kind == "question" || item.Kind == "approval") {
				if c := g.conn(node); c != nil {
					if frame, e := protocol.EncodeRequest(protocol.FrameWaitClear, protocol.NewID("d"), protocol.WaitClear{SessionID: item.SessionID}); e == nil {
						_ = c.write(ctx, frame)
					}
				}
			}
		}
		return map[string]any{"item": item}, nil

	case "lead.resolve":
		var p struct {
			Address string `json:"address"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad lead address")
		}
		id, err := st.ResolveLeadAddress(p.Address)
		return map[string]string{"session_id": id}, err
	case "msg.send":
		p, err := decode[protocol.MsgSend](raw)
		if err != nil {
			return nil, reject("bad msg.send")
		}
		owner, err := st.SessionNode(p.Sender)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			st.Event("msg.refused", nc.id, p.Sender, "", "sender session not on this node")
			g.logLine("msg.refused", "node", nc.id, "from", p.Sender, "to", p.Recipient, "bytes", len(p.Body),
				"reason", "sender session not on this node")
			return nil, reject("sender session %q does not belong to this node", p.Sender)
		}
		if p.IdempotencyKey == "" {
			p.IdempotencyKey = protocol.NewID("k")
		}
		// Only the worker's node, or a relay for a start the gateway tracked,
		// can mark a task. A peer's marker cannot change message routing.
		if p.WorkerTask {
			allowed, err := st.authorizeWorkerTask(nc.id, p)
			if err != nil {
				return nil, err
			}
			p.WorkerTask = allowed
		}
		// A message from its owner starts an ended worker again, before it is
		// stored, so a refusal reaches the sender (W10).
		workerTask, err := workerTaskForSend(st.db, p)
		if err != nil {
			return nil, err
		}
		if !workerTask {
			err = g.resumeEndedWorker(ctx, nc, p)
		}
		if err != nil {
			g.notifyResumeRefusal(ctx, p, err)
			var rj Rejection
			if errors.As(err, &rj) {
				st.Event("msg.refused", nc.id, p.Sender, "", err.Error())
				g.logLine("msg.refused", "node", nc.id, "from", p.Sender, "to", p.Recipient, "bytes", len(p.Body),
					"reason", err.Error())
			}
			return nil, err
		}
		m, created, route, err := st.StoreLeadMessage(p)
		if err != nil {
			var rj Rejection
			if errors.As(err, &rj) {
				st.Event("msg.refused", nc.id, p.Sender, "", err.Error())
				g.logLine("msg.refused", "node", nc.id, "from", p.Sender, "to", p.Recipient, "bytes", len(p.Body),
					"reason", err.Error())
			}
			return nil, err
		}
		started := false
		if created && m.State != protocol.StateHeld {
			started = g.dispatch(ctx, m)
		}
		if cur, ok, _ := st.Message(m.ID); ok {
			m = cur
		}
		// A reply to a handed-off request also stores the lead's copy.
		if created && m.Type == protocol.TypeReply {
			if cp, ok, _ := st.HandoffCopy(m.ID); ok {
				g.dispatch(ctx, cp)
			}
		}
		return map[string]any{"message": m, "routing": g.wakeReceiptNote(m, route, started)}, nil

	case "msg.handoff":
		p, err := decode[protocol.MsgHandoff](raw)
		if err != nil {
			return nil, reject("bad msg.handoff")
		}
		owner, err := st.SessionNode(p.Sender)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("sender session %q does not belong to this node", p.Sender)
		}
		m, err := st.StoreHandoff(p)
		if err != nil {
			var rj Rejection
			if errors.As(err, &rj) {
				st.Event("msg.refused", nc.id, p.Sender, "", err.Error())
			}
			return nil, err
		}
		if m.State == protocol.StateStored {
			g.dispatch(ctx, m)
		}
		if cur, ok, _ := st.Message(m.ID); ok {
			m = cur
		}
		return map[string]any{"message": m}, nil

	case "delivery.update":
		p, err := decode[protocol.DeliveryUpdate](raw)
		if err != nil {
			return nil, reject("bad delivery.update")
		}
		m, ok, err := st.Message(p.MessageID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown message %q", p.MessageID)
		}
		owner, err := st.SessionNode(m.To)
		if err != nil {
			return nil, err
		}
		if owner != nc.id {
			return nil, reject("message %q is not addressed to this node", p.MessageID)
		}
		notice, noticed, err := st.ApplyDeliveryReport(p.MessageID, p.State, p.Detail)
		if err != nil {
			return nil, err
		}
		if noticed {
			g.dispatch(ctx, notice)
		}
		return nil, nil

	case "outcome.resolve":
		p, err := decode[protocol.OutcomeResolution](raw)
		if err != nil {
			return nil, reject("bad resolution")
		}
		node, err := st.SessionNode(p.Owner)
		if err != nil || node != nc.id {
			return nil, reject("owner session not on this node")
		}
		m, err := st.ResolveOutcome(p.ReplyID, p.Owner, p.Action)
		if err != nil {
			return nil, err
		}
		if m.ID != "" {
			g.dispatch(ctx, m)
		}
		return map[string]any{"message": m}, nil

	case "query.outcome_inbox":
		entries, err := st.OutcomeInbox()
		if err != nil {
			return nil, err
		}
		return map[string]any{"entries": entries}, nil

	case "query.agents":
		p, err := decode[protocol.QueryAgents](raw)
		if err != nil {
			return nil, reject("bad query.agents")
		}
		agents, err := st.Agents(p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"agents": agents}, nil

	case protocol.FrameSessionWakeRebind:
		var p wakeRebind
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad wake rebind")
		}
		return nil, g.rebindWake(nc.id, p)

	case "query.agent":
		p, err := decode[protocol.QueryAgent](raw)
		if err != nil {
			return nil, reject("bad query.agent")
		}
		a, ok, err := st.Agent(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown agent %q", p.SessionID)
		}
		return map[string]any{"agent": a}, nil

	case "query.worker_log":
		p, err := decode[protocol.WorkerLogQuery](raw)
		if err != nil {
			return nil, reject("bad query.worker_log")
		}
		return st.WorkerLog(p.Limit)
	case "query.conversations":
		p, err := decode[protocol.QueryConversations](raw)
		if err != nil {
			return nil, reject("bad query.conversations")
		}
		page, err := st.ConversationsPage(p)
		if err != nil {
			return nil, err
		}
		return page, nil

	case "query.conversation_messages":
		p, err := decode[protocol.QueryConversationMessages](raw)
		if err != nil || p.ConversationID == "" {
			return nil, reject("bad query.conversation_messages")
		}
		ms, err := st.ConversationMessages(p.ConversationID, p.Limit)
		if err != nil {
			return nil, err
		}
		return protocol.ConversationMessagesResult{Messages: ms}, nil

	case "query.messages":
		p, err := decode[protocol.QueryMessages](raw)
		if err != nil {
			return nil, reject("bad query.messages")
		}
		ms, total, err := st.MessagesPage(nc.id, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"messages": ms, "total": total}, nil

	case "query.message":
		p, err := decode[protocol.QueryMessage](raw)
		if err != nil {
			return nil, reject("bad query.message")
		}
		m, ok, err := st.Message(p.MessageID)
		if err != nil {
			return nil, err
		}
		if ok {
			ok, err = st.CanSee(nc.id, m)
			if err != nil {
				return nil, err
			}
		}
		if !ok {
			return nil, reject("unknown message %q", p.MessageID)
		}
		m.CitedNotes, err = st.citedNotes(m)
		if err != nil {
			return nil, err
		}
		return map[string]any{"message": m}, nil
	}
	if data, ok, err := g.handleUsage(nc, typ, raw); ok {
		return data, err
	}
	if data, ok, err := g.handleToolRead(ctx, nc, typ, raw); ok {
		return data, err
	}
	if data, ok, err := g.handleDelivery(ctx, nc, typ, raw); ok {
		return data, err
	}
	return nil, reject("unknown frame type %q", typ)
}

// flushLeadRequests delivers the requests that the asks reconcile sent to team
// leads (a charter draft or a learned edge of a member node).
func (g *Gateway) flushLeadRequests(ctx context.Context) {
	for _, m := range g.store.TakeLeadRequests() {
		g.dispatch(ctx, m)
	}
}

// dispatch sends a stored message to its recipient's node if that node is
// connected and the session is online. It reports whether it started a wake of
// an offline recipient.
func (g *Gateway) dispatch(ctx context.Context, m protocol.Message) bool {
	// An ack closes a request without a delivery: nothing is sent and nothing
	// is woken.
	if m.Type == protocol.TypeAck {
		return false
	}
	a, ok, err := g.store.Agent(m.To)
	if err != nil || !ok || a.Harness == "bot" {
		return false
	}
	if !a.Online {
		return g.wakeForMessage(ctx, a, m)
	}
	g.sendDeliver(ctx, a.NodeID, m)
	return false
}

func (g *Gateway) sendDeliver(ctx context.Context, nodeID string, m protocol.Message) {
	if held, err := g.store.SessionAgentPending(m.To); err != nil || held {
		return
	}
	if m.From == "agent-net" && strings.HasPrefix(m.IdempotencyKey, "step_") {
		current, err := g.store.stepMessageCurrent(m.ID)
		if err != nil || !current {
			return
		}
	}
	if needsReminderCheck(m) {
		ok, err := g.store.reminderEligible(m.ParentID)
		if err != nil || !ok {
			return
		}
	}
	g.mu.Lock()
	nc := g.conns[nodeID]
	g.mu.Unlock()
	if nc == nil {
		return
	}
	// The gateway, and only the gateway, says whether the recipient is trusted.
	m.RecipientTrust = false
	if m.Type == protocol.TypeRequest {
		rule, err := g.store.RequestRule(m.From, m.To)
		if err != nil {
			g.logLine("error", "where", "request rule", "err", err)
			return
		}
		if rule == "ask" && !m.OwnerApproved {
			if _, err := g.store.db.Exec(`UPDATE messages SET state='held' WHERE id=? AND state IN ('stored','dispatched')`, m.ID); err != nil {
				g.logLine("error", "where", "hold message", "err", err)
			}
			return
		}
		m.RecipientTrust = rule == "auto"
	} else if a, ok, err := g.store.Agent(m.To); err == nil && ok {
		m.RecipientTrust = a.PeerTrust
	}
	// The sender identity comes from the stored session, never the message.
	// Bot output cannot carry the receiver's peer authorization.
	if m.FromHarness == "bot" {
		m.RecipientTrust = false
		m.ApprovalNote = botDeliveryNote(m.FromName)
	}
	// The relation from the sender's node to the recipient's node is graph
	// data, computed here and never set by a sender (decision 0022).
	m.RecipientRelation = g.relationForDelivery(m)
	// The order and advice labels come from the tree, never from the body.
	m.AuthorityNote = ""
	if note, err := g.store.authorityNote(m); err != nil {
		g.logLine("error", "where", "authority note", "err", err)
	} else {
		m.AuthorityNote = note
	}
	// The notes on cited messages are agent-net's own text (decision 0121).
	m.CitedNotes = nil
	if notes, err := g.store.citedNotes(m); err != nil {
		g.logLine("error", "where", "cited notes", "err", err)
	} else {
		m.CitedNotes = notes
	}
	authority, rule, err := g.store.MergeRuleForSession(m.To)
	if err != nil {
		g.logLine("error", "where", "recipient merge authority", "err", err)
		return
	}
	m.RecipientMergeAuthority = authority
	m.RecipientMergeText = rule
	m.RecipientMergeTextSet = true
	frame, err := protocol.EncodeRequest("msg.deliver", protocol.NewID("d"), protocol.MsgDeliver{Message: m})
	if err != nil {
		return
	}
	// Claim a due retry atomically, so concurrent ticks cannot resend it.
	if m.State == protocol.StateFailed {
		res, err := g.store.db.Exec(`UPDATE messages SET state='dispatched' WHERE id=? AND state='failed' AND expires_at>? AND EXISTS (SELECT 1 FROM delivery_retries WHERE message_id=messages.id AND next_at<=?)`, m.ID, g.store.now().UnixNano(), g.store.now().UnixNano())
		if err != nil {
			g.logLine("error", "where", "claim retry", "err", err)
			return
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return
		}
	}
	// Mark the message before the write. The node can answer "delivered" as soon
	// as it has the frame, and a message that is already delivered can no
	// longer be marked dispatched.
	if err := g.store.MarkDispatched(m.ID); err != nil {
		g.logLine("error", "where", "mark dispatched", "err", err)
	}
	if err := nc.write(ctx, frame); err != nil {
		if m.State == protocol.StateFailed {
			_, _ = g.store.db.Exec(`UPDATE messages SET state='failed' WHERE id=? AND state='dispatched'`, m.ID)
		} else {
			g.store.UnmarkDispatched(m.ID)
		}
	}
}

func (g *Gateway) dispatchPending(ctx context.Context, nodeID string, includeDispatched bool) {
	msgs, err := g.store.Pending(nodeID, includeDispatched)
	if err != nil {
		g.logLine("error", "where", "pending", "err", err)
		return
	}
	for _, m := range msgs {
		g.sendDeliver(ctx, nodeID, m)
	}
}

// The authenticated daemon supplies the actor after its local owner checks.
func graphActor(raw json.RawMessage) string {
	var p struct {
		By string `json:"by"`
	}
	if json.Unmarshal(raw, &p) == nil && (p.By == "cto" || p.By == "CTO (minor)" || p.By == "CTO (owner window)") {
		return p.By
	}
	if strings.HasPrefix(p.By, "owner (CTO proposal p_") && strings.HasSuffix(p.By, ")") {
		n := strings.TrimSuffix(strings.TrimPrefix(p.By, "owner (CTO proposal p_"), ")")
		if n != "" && strings.Trim(n, "0123456789") == "" {
			return p.By
		}
	}
	return "owner"
}
