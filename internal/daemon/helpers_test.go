package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---- gateway ----

type testGateway struct {
	t    *testing.T
	dir  string
	addr string
	gw   *gateway.Gateway
	srv  *httptest.Server
}

func startGateway(t *testing.T, dir, addr string) *testGateway {
	t.Helper()
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	var ln net.Listener
	var err error
	for i := 0; i < 100; i++ {
		if ln, err = net.Listen("tcp", addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(gateway.Options{DataDir: dir, OfflineGrace: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(gw.Handler())
	srv.Listener.Close()
	srv.Listener = ln
	srv.TLS = gw.TLSConfig()
	srv.StartTLS()
	g := &testGateway{t: t, dir: dir, addr: ln.Addr().String(), gw: gw, srv: srv}
	t.Cleanup(g.stop)
	return g
}

func (g *testGateway) stop() {
	if g.srv == nil {
		return
	}
	g.gw.Close()
	g.srv.Close()
	g.srv = nil
}

func (g *testGateway) joinString() string {
	code, err := g.gw.Store().CreateEnrollCode(10 * time.Minute)
	if err != nil {
		g.t.Fatal(err)
	}
	return fmt.Sprintf("https://%s#%s#%s", g.addr, code, g.gw.Fingerprint())
}

// ---- fake adapter ----

type fakeAdapter struct {
	harness string
	mu      sync.Mutex
	host    Host
	got     []protocol.Message
	fail    string
}

func (a *fakeAdapter) Harness() string { return a.harness }
func (a *fakeAdapter) Start(ctx context.Context, h Host) error {
	a.host = h
	return nil
}

func (a *fakeAdapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, m)
	if a.fail != "" {
		return Result{Detail: a.fail}
	}
	return Result{Delivered: true}
}

func (a *fakeAdapter) delivered() []protocol.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]protocol.Message(nil), a.got...)
}

func (a *fakeAdapter) setSessions(infos ...protocol.SessionInfo) {
	a.host.SetSessions(a.harness, infos)
}

// ---- node ----

type node struct {
	t       *testing.T
	d       *Daemon
	sock    string
	adapter *fakeAdapter
	cfg     Config
	cmdline string
}

// restart stops the daemon and starts a new one on the same socket and state.
func (n *node) restart() {
	n.t.Helper()
	n.d.Stop()
	d, err := New(n.cfg)
	if err != nil {
		n.t.Fatal(err)
	}
	if n.cmdline != "" {
		d.cmdline = func(int) string { return n.cmdline }
	}
	if err := d.Start(context.Background()); err != nil {
		n.t.Fatal(err)
	}
	n.d = d
	n.t.Cleanup(d.Stop)
	eventually(n.t, "daemon reconnected to gateway", d.Connected)
}

type nodeOpts struct {
	adapter   *fakeAdapter
	claudeDir string
	cmdline   string // what the fake process table says for every pid
}

func shortDir(t *testing.T) string {
	d, err := os.MkdirTemp("", "an")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func startNode(t *testing.T, g *testGateway, o nodeOpts) *node {
	t.Helper()
	cfgDir := t.TempDir()
	nc, ca, err := func() (NodeConfig, []byte, error) {
		if _, err := Join(context.Background(), g.joinString(), cfgDir); err != nil {
			return NodeConfig{}, nil, err
		}
		return LoadNode(cfgDir)
	}()
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(shortDir(t), "d.sock")
	cfg := Config{GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca,
		Hostname: "host-" + nc.NodeID, StateDir: t.TempDir(), SocketPath: sock,
		ClaudeSessionsDir: o.claudeDir, BackoffMin: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond,
		PluginAckTimeout: 500 * time.Millisecond}
	if o.adapter != nil {
		cfg.Adapters = []Adapter{o.adapter}
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if o.cmdline != "" {
		d.cmdline = func(int) string { return o.cmdline }
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := &node{t: t, d: d, sock: sock, adapter: o.adapter, cfg: cfg, cmdline: o.cmdline}
	t.Cleanup(d.Stop)
	eventually(t, "daemon connected to gateway", d.Connected)
	return n
}

// ---- local socket client that also sees pushes ----

type localClient struct {
	t      *testing.T
	c      net.Conn
	mu     sync.Mutex
	nextID int
	wait   map[string]chan map[string]json.RawMessage
	Pushes chan protocol.Message
	// AutoAck confirms every push with delivered ok.
	AutoAck bool
}

func dialLocal(t *testing.T, sock string) *localClient {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	lc := &localClient{t: t, c: c, wait: map[string]chan map[string]json.RawMessage{}, Pushes: make(chan protocol.Message, 16)}
	t.Cleanup(func() { c.Close() })
	go lc.read()
	return lc
}

func (lc *localClient) read() {
	sc := bufio.NewScanner(lc.c)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if raw, hasID := m["id"]; hasID {
			var id string
			json.Unmarshal(raw, &id)
			lc.mu.Lock()
			ch := lc.wait[id]
			lc.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		var method string
		json.Unmarshal(m["method"], &method)
		if method == "deliver" {
			var p struct {
				Message protocol.Message `json:"message"`
			}
			json.Unmarshal(m["params"], &p)
			lc.Pushes <- p.Message
			if lc.AutoAck {
				go lc.call("delivered", map[string]any{"message_id": p.Message.ID, "ok": true})
			}
		}
	}
}

func (lc *localClient) call(method string, params any) (json.RawMessage, error) {
	lc.mu.Lock()
	lc.nextID++
	id := fmt.Sprint(lc.nextID)
	ch := make(chan map[string]json.RawMessage, 1)
	lc.wait[id] = ch
	lc.mu.Unlock()
	line, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if _, err := lc.c.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if e, has := m["error"]; has {
			var s string
			json.Unmarshal(e, &s)
			return nil, errors.New(s)
		}
		return m["result"], nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("no response")
	}
}

func (lc *localClient) mustCall(method string, params any) json.RawMessage {
	lc.t.Helper()
	r, err := lc.call(method, params)
	if err != nil {
		lc.t.Fatalf("%s: %v", method, err)
	}
	return r
}

func (lc *localClient) nextPush() protocol.Message {
	lc.t.Helper()
	select {
	case m := <-lc.Pushes:
		return m
	case <-time.After(5 * time.Second):
		lc.t.Fatal("no push received")
		return protocol.Message{}
	}
}

// helloPi connects as a Pi plugin and returns the client and its session ID.
func helloPi(t *testing.T, n *node, nativeID, cwd string, autoAck bool) (*localClient, string) {
	t.Helper()
	lc := dialLocal(t, n.sock)
	lc.AutoAck = autoAck
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": nativeID, "cwd": cwd, "status": "idle"})
	var r struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(res, &r)
	if r.SessionID == "" {
		t.Fatal("hello returned no session_id")
	}
	return lc, r.SessionID
}

func messageState(t *testing.T, lc *localClient, id string) string {
	t.Helper()
	res, err := lc.call("message_status", map[string]any{"message_id": id})
	if err != nil {
		return "error: " + err.Error()
	}
	var r struct {
		Message protocol.Message `json:"message"`
	}
	json.Unmarshal(res, &r)
	return r.Message.State
}
