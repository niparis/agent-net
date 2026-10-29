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
}

type Gateway struct {
	opts   Options
	store  *Store
	ca     *authority
	cert   tls.Certificate
	fp     string
	pin    string
	logger *log.Logger

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
	return &Gateway{opts: opts, store: store, ca: ca, cert: cert, fp: fp, pin: pin, logger: logger,
		conns: map[string]*nodeConn{}, timers: map[string]*time.Timer{}}, nil
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
			if n, err := g.store.Expire(); err != nil {
				g.logger.Printf("expiry: %v", err)
			} else if n > 0 {
				g.logger.Printf("expired %d messages", n)
			}
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
	mux.HandleFunc("GET /install/bin/{os}/{arch}", g.handleInstallBin)
	mux.HandleFunc("GET /install/{code}", g.handleInstallScript)
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
		g.logger.Printf("enroll: %v", err)
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
	ws.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	nc := &nodeConn{id: nodeID, ws: ws, cancel: cancel}

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

	g.serve(ctx, nc)

	cancel()
	ws.Close(websocket.StatusNormalClosure, "")
	g.mu.Lock()
	if g.conns[nodeID] == nc && !g.closed {
		delete(g.conns, nodeID)
		g.timers[nodeID] = time.AfterFunc(g.opts.OfflineGrace, func() { g.nodeGone(nodeID) })
	}
	g.mu.Unlock()
	g.store.Event("node.disconnected", nodeID, "", "", "")
}

func (g *Gateway) nodeGone(nodeID string) {
	g.mu.Lock()
	_, live := g.conns[nodeID]
	closed := g.closed
	delete(g.timers, nodeID)
	g.mu.Unlock()
	if !live && !closed {
		if err := g.store.SetNodeOffline(nodeID); err != nil {
			g.logger.Printf("node offline: %v", err)
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
			continue
		}
		data, err := g.handle(ctx, nc, h.Type, raw)
		var reply []byte
		var eerr error
		if err != nil {
			var rj Rejection
			if !errors.As(err, &rj) {
				g.logger.Printf("node %s %s: %v", nc.id, h.Type, err)
			}
			reply, eerr = protocol.EncodeResult(h.ID, nil, err)
		} else {
			reply, eerr = protocol.EncodeResult(h.ID, data, nil)
		}
		if eerr != nil {
			g.logger.Printf("encode result: %v", eerr)
			continue
		}
		if nc.write(ctx, reply) != nil {
			return
		}
		if h.Type == "session.sync" && err == nil {
			g.dispatchPending(ctx, nc.id, true)
		}
		if h.Type == "session.upsert" && err == nil {
			g.dispatchPending(ctx, nc.id, false)
		}
	}
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
		if err := st.SetNodeHostname(nc.id, p.Hostname); err != nil {
			return nil, err
		}
		return map[string]string{"node_id": nc.id}, nil

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
			return nil, reject("sender session %q does not belong to this node", p.Sender)
		}
		m, created, err := st.StoreMessage(p)
		if err != nil {
			var rj Rejection
			if errors.As(err, &rj) {
				st.Event("msg.refused", nc.id, p.Sender, "", err.Error())
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
	frame, err := protocol.EncodeRequest("msg.deliver", protocol.NewID("d"), protocol.MsgDeliver{Message: m})
	if err != nil {
		return
	}
	if err := nc.write(ctx, frame); err != nil {
		return
	}
	if err := g.store.MarkDispatched(m.ID); err != nil {
		g.logger.Printf("mark dispatched: %v", err)
	}
}

func (g *Gateway) dispatchPending(ctx context.Context, nodeID string, includeDispatched bool) {
	msgs, err := g.store.Pending(nodeID, includeDispatched)
	if err != nil {
		g.logger.Printf("pending: %v", err)
		return
	}
	for _, m := range msgs {
		g.sendDeliver(ctx, nodeID, m)
	}
}
