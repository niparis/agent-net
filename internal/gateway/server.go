// Package gateway is the network server: enrollment, the node WebSocket,
// routing and storage.
package gateway

import (
	"context"
	"crypto/tls"
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
}

type Gateway struct {
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
	// importMu serializes MCP imports, so two applies cannot race on a name.
	importMu sync.Mutex
	skills   *skillStore
	ca       *authority
	cert     tls.Certificate
	fp       string
	pin      string
	logger   *log.Logger
	// adminToken guards /v1/admin. It is new on every start and is written
	// to admin.json in the data dir, which only the local user can read.
	adminToken string

	// usage holds the newest CPU and memory report of each node (usage.go).
	usage usageTable

	mu                sync.Mutex
	conns             map[string]*nodeConn
	timers            map[string]*offlineTimer
	closed            bool
	appsDone          chan struct{}
	rotationMu        sync.Mutex
	machineRotationMu sync.Mutex
	machineAuthMu     sync.Mutex // authentication, removal and confirmation ordering
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
	id        string
	tokenHash string // credential used for this connection
	ws        *websocket.Conn
	wmu       sync.Mutex
	cancel    context.CancelFunc
	dead      chan struct{} // closed when the connection ends
	health    *linkkeepalive.Monitor

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

func (nc *nodeConn) write(ctx context.Context, b []byte) error {
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
	g := &Gateway{limiter: ratelimit.New(ratelimit.Config{}, opts.Now), opts: opts, store: store, ca: ca, cert: cert, fp: fp, pin: pin, logger: logger,
		appsDone: make(chan struct{}), adminToken: protocol.NewToken(), conns: map[string]*nodeConn{}, timers: map[string]*offlineTimer{},
		enrollLimit: newEnrollLimiter(opts.Now)}
	store.settingsHook = func() { go g.settingsChanged() }
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
	conns := make([]*nodeConn, 0, len(g.conns))
	for _, c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	for _, c := range conns {
		c.ws.Close(websocket.StatusGoingAway, "gateway closing")
		c.cancel()
	}
	closeAllAppSessions(g.store)
	// Give the read loops a moment to leave before the database goes away.
	time.Sleep(50 * time.Millisecond)
	return g.store.Close()
}

// RunExpiry expires overdue messages once a minute until ctx ends.
func (g *Gateway) RunExpiry(ctx context.Context) {
	go g.RunRetries(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	appsCleanup := time.NewTicker(10 * time.Minute)
	defer appsCleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.appsDone:
			return
		case <-appsCleanup.C:
			now := g.store.now()
			if err := g.store.DeleteExpiredTokens(now); err != nil {
				g.logLine("error", "where", "app token cleanup", "err", err)
			}
			if err := g.store.CleanupOAuth(now); err != nil {
				g.logLine("error", "where", "oauth cleanup", "err", err)
			}
		case <-t.C:
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
			checks, err := g.store.checkIdle()
			if err != nil {
				g.logLine("error", "where", "idle checks", "err", err)
			}
			for _, m := range checks {
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
		}
	}
}

// RunLearning processes suggestions independently of message expiry because a
// phrasing worker can take up to three minutes to finish.
func (g *Gateway) RunLearning(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := g.store.ProcessLearnedContext(ctx); err != nil && ctx.Err() == nil {
			g.logLine("error", "where", "learned suggestions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RetentionInterval is how often RunRetention deletes old data.
const RetentionInterval = time.Hour

// RunRetention deletes data older than Options.Retention, once at start and
// then every RetentionInterval, until ctx ends.
func (g *Gateway) RunRetention(ctx context.Context) {
	t := time.NewTicker(RetentionInterval)
	defer t.Stop()
	for {
		if _, err := g.store.Prune(g.opts.Retention); err != nil {
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
	mux.HandleFunc("GET /v1/worker-providers", g.handleWorkerProviders)
	mux.HandleFunc("PUT /v1/worker-providers", g.handleWorkerProviders)
	mux.HandleFunc("GET /v1/difficulty", g.handleDifficulty)
	mux.HandleFunc("PUT /v1/difficulty", g.handleDifficulty)
	mux.HandleFunc("GET /v1/scores", g.handleScoresGet)
	mux.HandleFunc("PUT /v1/scores", g.handleScoresSet)
	mux.HandleFunc("GET /v1/worker-rotation", g.handleWorkerRotation)
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
	for {
		_, raw, err := nc.health.Read(ctx, nc.ws)
		if err != nil {
			return
		}
		var h protocol.Header
		if json.Unmarshal(raw, &h) != nil {
			continue
		}
		if h.Type == "result" {
			// Results to msg.deliver only say "accepted"; delivery.update has the outcome.
			// Results to the gateway's own calls (node.upgrade) go to their waiter.
			nc.answer(h)
			continue
		}
		if h.Type == "machine.rotate" || h.Type == "machine.remove" || h.Type == "session.sync" || h.Type == "session.upsert" || h.Type == "project.add" || h.Type == "tools.new_projects" || h.Type == "tools.toggle" || h.Type == "cto.proposal.validate" || h.Type == "tools.read_file" || h.Type == "tools.import" || h.Type == "tools.import_folder" || h.Type == "tools.keep_copy" || h.Type == "gateway.backup" || h.Type == "tools.plan" || h.Type == "tools.apply" || h.Type == "tools.undo" || h.Type == "tools.skill_details" || h.Type == "tools.summary" {
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
		if isSessionRelay(h.Type) {
			// The target machine may ask this gateway a question while it acts,
			// so the relay must not run in this node's read loop.
			go g.answer(ctx, nc, h, raw)
			continue
		}
		if h.Type == "msg.send" && g.msgSendMayResume(raw) {
			// Starting an ended worker again asks this same node and waits for
			// its answer, so it must not run in the read loop that reads it.
			go g.answer(ctx, nc, h, raw)
			continue
		}
		if g.answer(ctx, nc, h, raw) != nil {
			return
		}
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
	a, ok, err := g.store.Agent(p.Recipient)
	return err == nil && ok && a.Worker != nil
}

// answer handles one frame and writes its result. A link error ends the loop.
func (g *Gateway) answer(ctx context.Context, nc *nodeConn, h protocol.Header, raw []byte) error {
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
	if nc.write(ctx, reply) != nil {
		return errNodeGone
	}
	if (h.Type == "machine.rotate" || h.Type == "machine.remove") && err == nil {
		if result, ok := data.(map[string]bool); ok && result["reconnect"] {
			nc.ws.Close(websocket.StatusServiceRestart, "machine credentials changed")
		}
	}
	if h.Type == "session.sync" && err == nil {
		g.sendMCPSync(ctx, nc)
		g.sendSettingsSync(ctx, nc)
		g.dispatchPending(ctx, nc.id, true)
	}
	if h.Type == "session.upsert" && err == nil {
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
	if st.machineRemoved(nc.id) {
		return nil, reject("this machine was removed from the network; run agent-net setup to join again")
	}
	if strings.HasPrefix(typ, "xfer.") {
		return g.handleTransfer(nc, typ, raw)
	}
	if typ == "session.sync" || typ == "session.upsert" || typ == "project.add" {
		// Freeze defaults before discovery so existing off cells stay off.
		if _, err := st.ToolProjectStates(); err != nil {
			return nil, err
		}
		defer func() {
			if err := g.addNewSkillProjects(ctx); err != nil {
				g.logLine("skills.standing_choice.refused", "error", err)
			}
		}()
	}
	switch typ {
	case "cto.session.register":
		p, err := decode[struct {
			Owner string `json:"owner"`
		}](raw)
		if err != nil || !g.validToolOwner(nc, p.Owner) {
			return nil, reject("owner authority required")
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
		if err := st.SetNodeHello(nc.id, p.Hostname, p.Version); err != nil {
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
		graph, err := st.Graph()
		if err != nil {
			return nil, err
		}
		needs := []protocol.ToolNeed{}
		for _, node := range graph.Nodes {
			if node.Charter.Needs != "" {
				needs = append(needs, protocol.ToolNeed{Node: node.Name, Text: node.Charter.Needs, State: "unverified"})
			}
		}
		return needs, nil
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
		return nil, st.SetNodeLaunch(nc.id, p.Launch)

	case protocol.FrameSessionStart, protocol.FrameSessionProposalStart, protocol.FrameSessionResume, protocol.FrameSessionStop, protocol.FrameSessionGet:
		return g.handleSessionRelay(ctx, nc, typ, raw)

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

	case "session.sync":
		p, err := decode[protocol.SessionSync](raw)
		if err != nil {
			return nil, reject("bad session.sync")
		}
		out, err := st.SyncSessions(nc.id, p.Sessions)
		if err != nil {
			return nil, err
		}
		res := make([]map[string]string, 0, len(out))
		for _, s := range out {
			res = append(res, map[string]string{"native_id": s.NativeID, "harness": s.Harness, "session_id": s.SessionID})
		}
		return map[string]any{"sessions": res}, nil

	case "session.upsert":
		p, err := decode[protocol.SessionUpsert](raw)
		if err != nil {
			return nil, reject("bad session.upsert")
		}
		id, err := st.UpsertSession(nc.id, p.Session)
		if err != nil {
			return nil, err
		}
		return map[string]string{"session_id": id}, nil

	case "session.remove":
		p, err := decode[protocol.SessionRemove](raw)
		if err != nil {
			return nil, reject("bad session.remove")
		}
		return nil, st.SetSessionOffline(nc.id, p.SessionID)

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
	case "graph.get":
		p, err := decode[protocol.QueryGraph](raw)
		if err != nil {
			return nil, reject("bad graph.get")
		}
		return st.visibleGraph(p.All)
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
		tx, err := st.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := setCharterTx(tx, p.Node, p.Attr, p.Value, "owner", st.now().UnixNano()); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case "charter.set":
		p, err := decode[protocol.CharterSetRequest](raw)
		if err != nil {
			return nil, reject("bad charter.set")
		}
		if err := st.SetCharter(p.Node, p.Attr, p.Value, graphActor(raw)); err != nil {
			return nil, err
		}
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
		if err != nil || p.Node == "" {
			return nil, reject("bad charter.history")
		}
		return st.CharterHistory(p.Node, p.Limit)
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
		if typ == "edge.set" {
			err = st.SetEdge(p, graphActor(raw))
		} else {
			err = st.RemoveEdge(p, graphActor(raw))
		}
		return map[string]bool{"ok": true}, err
	case "edge.history":
		p, err := decode[protocol.GraphHistoryRequest](raw)
		if err != nil {
			return nil, reject("bad edge.history")
		}
		return st.EdgeHistory(p.Node, p.Limit)
	case "graph.undo":
		p, err := decode[protocol.GraphUndoFrame](raw)
		if err != nil || p.HistoryID < 0 {
			return nil, reject("bad graph.undo")
		}
		return map[string]bool{"ok": true}, st.GraphUndo(p.HistoryID, graphActor(raw))
	case "settings.snapshot":
		return st.SettingsSnapshot()
	case "settings.write":
		p, err := decode[protocol.SettingsWrite](raw)
		if err != nil || len(p.Changes) == 0 {
			return nil, reject("bad settings.write")
		}
		if !settingsActor(p.By) {
			return nil, reject("invalid settings actor")
		}
		return st.WriteSettings(p.Changes, p.By, p.Reason)
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
		return st.UndoSettings(p.Revision, p.By)
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
			return map[string]bool{"ok": true}, st.DecideLearned(p.ID, p.Action)
		default:
			return map[string]bool{"ok": true}, st.MarkLearnedViewed(p.Node, p.ID)
		}

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
		return protocol.PolicyGot{Harness: a.Harness, Delegation: a.Delegation, Project: projectOfSession(a)}, nil

	case "profile.announce":
		p, err := decode[protocol.ProfileAnnounce](raw)
		if err != nil {
			return nil, reject("bad profile.announce")
		}
		prof, err := st.Announce(nc.id, p)
		if err != nil {
			return nil, err
		}
		// An announce can answer an idle check with "more", which stores one
		// autonomy wake. Queue it on this node now.
		g.dispatchPending(ctx, nc.id, false)
		return map[string]any{"profile": prof}, nil

	case "inbox.create":
		var p struct {
			SessionID      string `json:"session_id"`
			Kind           string `json:"kind"`
			Text           string `json:"text"`
			Recommendation string `json:"recommendation"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, reject("bad inbox.create")
		}
		if p.Kind != "approval" && p.Kind != "feature_idea" {
			return nil, reject("invalid inbox kind")
		}
		agent, ok, err := st.Agent(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !ok || agent.NodeID != nc.id || agent.Harness == protocol.OwnerHarness {
			return nil, reject("session does not belong to this node")
		}
		var item protocol.InboxItem
		if p.Kind == "approval" {
			item, err = st.RequestWrittenApproval(agent.SessionID, agent.Repo.Project, protocol.ApprovalRequest{Action: p.Text, Recommendation: p.Recommendation})
		} else {
			item, err = st.CreateInboxItem(protocol.InboxItem{Kind: p.Kind, SessionID: agent.SessionID, Node: agent.Repo.Project, Text: p.Text, Recommendation: p.Recommendation})
		}
		return map[string]any{"item": item}, err

	case "apps.public.status", "apps.public.on", "apps.public.off", "apps.public.url":
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
		if err := st.db.QueryRow(`SELECT text FROM inbox_items WHERE message_id=?`, "app-approval:"+p.ID).Scan(&text); err == nil && g.logger != nil {
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

	case "inbox.list":
		items, err := st.ListInbox()
		return map[string]any{"items": items}, err

	case "inbox.answer":
		var p struct {
			ID     string `json:"item_id"`
			Answer string `json:"answer"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID == "" {
			g.logLine("inbox.answer.refused", "id", p.ID, "reason", "bad inbox.answer")
			return nil, reject("bad inbox.answer")
		}
		item, err := st.AnswerInbox(p.ID, p.Answer)
		if err != nil {
			g.logLine("inbox.answer.refused", "id", p.ID, "reason", err.Error())
			return nil, err
		}
		node, err := st.SessionNode(item.SessionID)
		if err == nil {
			g.dispatchPending(ctx, node, false)
		}
		return map[string]any{"item": item}, nil

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
		// A message from its owner starts an ended worker again, before it is
		// stored, so a refusal reaches the sender (W10).
		if err := g.resumeEndedWorker(ctx, nc, p); err != nil {
			g.notifyResumeRefusal(ctx, p, err)
			var rj Rejection
			if errors.As(err, &rj) {
				st.Event("msg.refused", nc.id, p.Sender, "", err.Error())
				g.logLine("msg.refused", "node", nc.id, "from", p.Sender, "to", p.Recipient, "bytes", len(p.Body),
					"reason", err.Error())
			}
			return nil, err
		}
		m, created, err := st.StoreMessage(p)
		if err != nil {
			var rj Rejection
			if errors.As(err, &rj) {
				st.Event("msg.refused", nc.id, p.Sender, "", err.Error())
				g.logLine("msg.refused", "node", nc.id, "from", p.Sender, "to", p.Recipient, "bytes", len(p.Body),
					"reason", err.Error())
			}
			return nil, err
		}
		if created && m.State != protocol.StateHeld {
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
		return map[string]any{"message": m}, nil
	}
	if data, ok, err := g.handleUsage(nc, typ, raw); ok {
		return data, err
	}
	if data, ok, err := g.handleToolRead(nc, typ, raw); ok {
		return data, err
	}
	if data, ok, err := g.handleDelivery(nc, typ, raw); ok {
		return data, err
	}
	return nil, reject("unknown frame type %q", typ)
}

// dispatch sends a stored message to its recipient's node if that node is
// connected and the session is online.
func (g *Gateway) dispatch(ctx context.Context, m protocol.Message) {
	a, ok, err := g.store.Agent(m.To)
	if err != nil || !ok || !a.Online || a.Harness == "bot" {
		return
	}
	g.sendDeliver(ctx, a.NodeID, m)
}

func (g *Gateway) sendDeliver(ctx context.Context, nodeID string, m protocol.Message) {
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
