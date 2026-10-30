// Package gateway is the network server: enrollment, the node WebSocket,
// routing and storage.
package gateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	"github.com/niparis/agent-net/internal/protocol"
)

type Options struct {
	DataDir string
	// DistDir holds the agent-net-{os}-{arch} binaries that /install serves.
	DistDir string
	// SANs are extra host names or IPs for the server certificate.
	SANs []string
	// OfflineGrace is how long a node may be disconnected before its
	// sessions go offline. Default 30 s.
	OfflineGrace time.Duration
	Logger       *log.Logger
	// Now is for tests.
	Now func() time.Time
	// Version is the agent-net version this gateway runs (served at
	// /install/version and listed with the nodes).
	Version string
	// Restart restarts the gateway process. The admin restart call uses it
	// when there is no service manager. Nil means that call is refused.
	Restart func()
	// Retention says how long data is kept. The zero value keeps everything.
	Retention Retention
}

type Gateway struct {
	opts   Options
	store  *Store
	ca     *authority
	cert   tls.Certificate
	fp     string
	pin    string
	logger *log.Logger
	// adminToken guards /v1/admin. It is new on every start and is written
	// to admin.json in the data dir, which only the local user can read.
	adminToken string

	// usage holds the newest CPU and memory report of each node (usage.go).
	usage usageTable

	mu     sync.Mutex
	conns  map[string]*nodeConn
	timers map[string]*time.Timer
	closed bool
}

type nodeConn struct {
	id     string
	ws     *websocket.Conn
	wmu    sync.Mutex
	cancel context.CancelFunc
	dead   chan struct{} // closed when the connection ends

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
	if opts.OfflineGrace == 0 {
		opts.OfflineGrace = 30 * time.Second
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
	g := &Gateway{opts: opts, store: store, ca: ca, cert: cert, fp: fp, pin: pin, logger: logger,
		adminToken: protocol.NewToken(), conns: map[string]*nodeConn{}, timers: map[string]*time.Timer{}}
	store.logf = g.logLine
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
	g.mu.Lock()
	g.closed = true
	for _, t := range g.timers {
		t.Stop()
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
	// Give the read loops a moment to leave before the database goes away.
	time.Sleep(50 * time.Millisecond)
	return g.store.Close()
}

// RunExpiry expires overdue messages once a minute until ctx ends.
func (g *Gateway) RunExpiry(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Expire writes one log line per message.
			if _, err := g.store.Expire(); err != nil {
				g.logLine("error", "where", "expiry", "err", err)
			}
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
	mux.HandleFunc("GET /v1/cto-settings", g.handleCTOSettingsGet)
	mux.HandleFunc("PUT /v1/cto-settings", g.handleCTOSettingsSet)
	mux.HandleFunc("GET /v1/integrations", g.handleIntegrationsGet)
	mux.HandleFunc("PUT /v1/integrations", g.handleIntegrationsSet)
	mux.HandleFunc("GET /install/bin/{os}/{arch}", g.handleInstallBin)
	mux.HandleFunc("GET /install/{code}", g.handleInstallScript)
	g.upgradeRoutes(mux)
	g.recordRoutes(mux)
	return mux
}

func (g *Gateway) handleEnroll(w http.ResponseWriter, r *http.Request) {
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
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	nodeID, ok, err := g.store.NodeByToken(token)
	if err != nil || !ok || token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	nc := &nodeConn{id: nodeID, ws: ws, cancel: cancel, dead: make(chan struct{})}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		ws.Close(websocket.StatusGoingAway, "gateway closing")
		cancel()
		return
	}
	old := g.conns[nodeID]
	g.conns[nodeID] = nc
	if t := g.timers[nodeID]; t != nil {
		t.Stop()
		delete(g.timers, nodeID)
	}
	g.mu.Unlock()
	if old != nil {
		old.ws.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
		old.cancel()
	}
	g.store.Event("node.connected", nodeID, "", "", r.RemoteAddr)
	g.logLine("node.connected", "node", nodeID, "host", g.store.hostname(nodeID), "remote", r.RemoteAddr)

	g.serve(ctx, nc)

	close(nc.dead)
	cancel()
	ws.Close(websocket.StatusNormalClosure, "")
	g.mu.Lock()
	if g.conns[nodeID] == nc && !g.closed {
		delete(g.conns, nodeID)
		g.timers[nodeID] = time.AfterFunc(g.opts.OfflineGrace, func() { g.nodeGone(nodeID) })
	}
	g.mu.Unlock()
	g.store.Event("node.disconnected", nodeID, "", "", "")
	g.logLine("node.disconnected", "node", nodeID, "host", g.store.hostname(nodeID))
}

func (g *Gateway) nodeGone(nodeID string) {
	g.mu.Lock()
	_, live := g.conns[nodeID]
	closed := g.closed
	delete(g.timers, nodeID)
	g.mu.Unlock()
	if !live && !closed {
		if err := g.store.SetNodeOffline(nodeID); err != nil {
			g.logLine("error", "where", "node offline", "err", err)
		}
	}
}

func (g *Gateway) serve(ctx context.Context, nc *nodeConn) {
	for {
		_, raw, err := nc.ws.Read(ctx)
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

// msgSendMayResume tells whether a msg.send is addressed to an ended worker.
// Only then can the gateway ask the machine to start the worker again.
func (g *Gateway) msgSendMayResume(raw []byte) bool {
	var p protocol.MsgSend
	if json.Unmarshal(raw, &p) != nil || p.Recipient == "" {
		return false
	}
	a, ok, err := g.store.Agent(p.Recipient)
	return err == nil && ok && a.Worker != nil && !a.Online
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
	if h.Type == "session.sync" && err == nil {
		g.sendMCPSync(ctx, nc)
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
	switch typ {
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
		// The daemon uses the name the gateway lists (the owner's display name
		// when there is one) where it compares host names.
		return map[string]string{"node_id": nc.id, "hostname": st.hostname(nc.id)}, nil

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
		g.logLine("node.renamed", "by", nc.id, "node", p.NodeID, "name", res.DisplayName)
		if c := g.conn(p.NodeID); c != nil {
			if frame, err := protocol.EncodeRequest("node.name", protocol.NewID("d"), protocol.NodeName{Hostname: res.Hostname}); err == nil {
				c.write(ctx, frame)
			}
		}
		return res, nil

	case "node.harnesses":
		p, err := decode[protocol.NodeHarnesses](raw)
		if err != nil || p.Harnesses == nil {
			return nil, reject("bad node.harnesses")
		}
		if _, err = st.SetNodeHarnesses(nc.id, p.Harnesses); err != nil {
			return nil, err
		}
		return nil, st.SetNodeLaunch(nc.id, p.Launch)

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

	case "query.graph":
		return g.handleGraphView(raw)

	case "query.events":
		p, err := decode[protocol.QueryEvents](raw)
		if err != nil {
			return nil, reject("bad query.events")
		}
		evs, err := st.FeedEvents(p.AfterID, p.Limit)
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

	case "session.trust":
		p, err := decode[protocol.SessionTrust](raw)
		if err != nil || p.SessionID == "" {
			return nil, reject("bad session.trust")
		}
		ok, err := st.SetPeerTrust(p.SessionID, p.On)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, reject("unknown or untrustable session %q", p.SessionID)
		}
		st.Event("session.trust", nc.id, p.SessionID, "", "on="+strconv.FormatBool(p.On))
		g.logLine("session.trust", "node", nc.id, "session", p.SessionID, "on", p.On)
		return map[string]any{"session_id": p.SessionID, "on": p.On}, nil

	case "cto.capabilities":
		return map[string]bool{"attribution": true}, nil
	case "graph.get":
		return st.Graph()
	case "charter.set":
		p, err := decode[protocol.CharterSetRequest](raw)
		if err != nil {
			return nil, reject("bad charter.set")
		}
		return map[string]bool{"ok": true}, st.SetCharter(p.Node, p.Attr, p.Value, graphActor(raw))
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
		return protocol.PolicyGot{Harness: a.Harness, Delegation: a.Delegation}, nil

	case "profile.announce":
		p, err := decode[protocol.ProfileAnnounce](raw)
		if err != nil {
			return nil, reject("bad profile.announce")
		}
		prof, err := st.Announce(nc.id, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"profile": prof}, nil

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
		// A message from its owner starts an ended worker again, before it is
		// stored, so a refusal reaches the sender (W10).
		if err := g.resumeEndedWorker(ctx, nc, p); err != nil {
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
		if created {
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
		return nil, st.ApplyDelivery(p.MessageID, p.State, p.Detail)

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

	case "query.messages":
		p, err := decode[protocol.QueryMessages](raw)
		if err != nil {
			return nil, reject("bad query.messages")
		}
		ms, err := st.Messages(nc.id, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"messages": ms}, nil

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
	return nil, reject("unknown frame type %q", typ)
}

// dispatch sends a stored message to its recipient's node if that node is
// connected and the session is online.
func (g *Gateway) dispatch(ctx context.Context, m protocol.Message) {
	a, ok, err := g.store.Agent(m.To)
	if err != nil || !ok || !a.Online {
		return
	}
	g.sendDeliver(ctx, a.NodeID, m)
}

func (g *Gateway) sendDeliver(ctx context.Context, nodeID string, m protocol.Message) {
	g.mu.Lock()
	nc := g.conns[nodeID]
	g.mu.Unlock()
	if nc == nil {
		return
	}
	// The gateway, and only the gateway, says whether the recipient is trusted.
	m.RecipientTrust = false
	if a, ok, err := g.store.Agent(m.To); err == nil && ok {
		m.RecipientTrust = a.PeerTrust
	}
	frame, err := protocol.EncodeRequest("msg.deliver", protocol.NewID("d"), protocol.MsgDeliver{Message: m})
	if err != nil {
		return
	}
	// Mark the message before the write. The node can answer "delivered" as soon
	// as it has the frame, and a message that is already delivered can no
	// longer be marked dispatched.
	if err := g.store.MarkDispatched(m.ID); err != nil {
		g.logLine("error", "where", "mark dispatched", "err", err)
	}
	if err := nc.write(ctx, frame); err != nil {
		g.store.UnmarkDispatched(m.ID)
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
	if json.Unmarshal(raw, &p) == nil && p.By == "cto" {
		return "cto"
	}
	return "owner"
}
