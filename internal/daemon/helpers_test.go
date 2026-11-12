package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// testScale is the factor for every wait that is not a negative check. It is 3
// with the race detector (processes start slowly) and can be raised with
// AGENT_NET_TEST_SCALE when the machine is loaded, e.g. AGENT_NET_TEST_SCALE=4.
var testScale = func() float64 {
	f := 1.0
	if raceOn {
		f = 3
	}
	if v, err := strconv.ParseFloat(os.Getenv("AGENT_NET_TEST_SCALE"), 64); err == nil && v > 0 {
		f = v
	}
	return f
}()

// scaled stretches a wait for slow machines. Use it for the time a test gives
// something to happen, never for the time it gives something not to happen.
func scaled(d time.Duration) time.Duration { return time.Duration(float64(d) * testScale) }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(scaled(20 * time.Second))
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitGraphCutover waits for the gateway to run the automatic typed Graph
// cutover after the test machines report a typed hello.
func waitGraphCutover(t *testing.T, g *testGateway) {
	t.Helper()
	eventually(t, "typed Graph cutover", func() bool {
		_, err := g.gw.Store().GraphForRef(nil)
		return err == nil
	})
}

// ---- gateway ----

type testGateway struct {
	t    *testing.T
	dir  string
	addr string
	gw   *gateway.Gateway
	srv  *httptest.Server
	logs *logBuf
}

// logBuf collects the gateway's log lines.
type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// String returns everything logged so far, for a failure message.
func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// lines returns the log lines that hold every given text.
func (l *logBuf) lines(parts ...string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range strings.Split(l.b.String(), "\n") {
		ok := line != ""
		for _, p := range parts {
			ok = ok && strings.Contains(line, p)
		}
		if ok {
			out = append(out, line)
		}
	}
	return out
}

func startGateway(t *testing.T, dir, addr string) *testGateway {
	return startGatewayPageSize(t, dir, addr, 0)
}

func startGatewayWithToolsPageSize(t *testing.T, dir string, size int) *testGateway {
	return startGatewayPageSize(t, dir, "", size)
}

func startGatewayPageSize(t *testing.T, dir, addr string, size int) *testGateway {
	return startGatewayWith(t, dir, addr, size, nil)
}

// startGatewayWith starts a gateway and lets the caller change its options.
func startGatewayWith(t *testing.T, dir, addr string, size int, mutate func(*gateway.Options)) *testGateway {
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
	logs := &logBuf{}
	seedDatabase(t, dir, "gateway.db", gatewayTemplateBytes)
	opts := gateway.Options{DataDir: dir, ToolsPageSize: size, OfflineGrace: 200 * time.Millisecond,
		Logger: log.New(logs, "", 0),
		// The test sessions live in the test's own temporary folders.
		TempFolders: []string{},
		// These tests exercise other worker behavior with short fake tasks.
		TestNoBriefChecks: true}
	if mutate != nil {
		mutate(&opts)
	}
	gw, err := gateway.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(gw.Handler())
	srv.Listener.Close()
	srv.Listener = ln
	srv.TLS = gw.TLSConfig()
	srv.StartTLS()
	g := &testGateway{t: t, dir: dir, addr: ln.Addr().String(), gw: gw, srv: srv, logs: logs}
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
	// uncertain makes Deliver report a write that failed after connecting.
	uncertain string
	// crash makes Deliver end its goroutine during the write, as if the
	// daemon process died there: nothing after the write runs.
	crash bool
}

func (a *fakeAdapter) Harness() string { return a.harness }
func (a *fakeAdapter) Start(ctx context.Context, h Host) error {
	a.host = h
	return nil
}

func (a *fakeAdapter) Deliver(ctx context.Context, target protocol.SessionInfo, m protocol.Message) Result {
	a.mu.Lock()
	a.got = append(a.got, m)
	crash := a.crash
	a.mu.Unlock()
	if crash {
		runtime.Goexit()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != "" {
		return Result{Detail: a.fail}
	}
	if a.uncertain != "" {
		return Result{Uncertain: true, Detail: a.uncertain}
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
	exe     string
}

// restart stops the daemon and starts a new one on the same socket and state.
func (n *node) restart() {
	n.t.Helper()
	n.restartWith(nil, true)
}

// restartWith is restart that runs between, if set, after the old daemon has
// stopped and before the new one starts. Without connect it does not wait for
// the gateway, which may be down.
func (n *node) restartWith(between func(), connect bool) {
	n.t.Helper()
	n.d.Stop()
	if between != nil {
		between()
	}
	d, err := New(n.cfg)
	if err != nil {
		n.t.Fatal(err)
	}
	d.inSession = func(int) bool { return false }
	if n.cmdline != "" {
		d.cmdline = func(int) string { return n.cmdline }
	}
	if n.exe != "" {
		d.exe = func(int) string { return n.exe }
	}
	if err := d.Start(context.Background()); err != nil {
		n.t.Fatal(err)
	}
	n.d = d
	n.t.Cleanup(d.Stop)
	if connect {
		eventually(n.t, "daemon reconnected to gateway", d.Connected)
	}
}

type nodeOpts struct {
	adapter   *fakeAdapter
	claudeDir string
	cmdline   string // what the fake process table says for every pid
	exe       string // the executable path the fake process table says for every pid
	// detect replaces the harness detection; interval is how often it runs.
	detect   func(ctx context.Context) []protocol.HarnessInfo
	interval time.Duration
	logger   *log.Logger
	// cfg, if set, changes the daemon config before the daemon is made.
	cfg func(*Config)
	// realGate makes the daemon use the real inside-a-session check.
	realGate bool
}

func shortDir(t *testing.T) string {
	d, err := os.MkdirTemp("", "an")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// isolateDaemonCaches keeps a test daemon away from the real Go and uv caches.
// The real worker cache is /tmp/agent-net-gocache, which is under the temp
// folder, so the cache variables always get fresh test folders. HOME is kept
// only when the test already chose one under its own temp root.
func isolateDaemonCaches(t *testing.T) {
	t.Helper()
	goCache := t.TempDir()
	t.Setenv("AGENT_NET_GOCACHE", goCache)
	t.Setenv("GOCACHE", goCache)
	t.Setenv("AGENT_NET_GOTMPDIR", t.TempDir())
	t.Setenv("UV_CACHE_DIR", t.TempDir())
	root := filepath.Dir(t.TempDir())
	if home := os.Getenv("HOME"); pathWithin(root, home) {
		t.Setenv("HOME", home)
		return
	}
	t.Setenv("HOME", t.TempDir())
}

func pathWithin(base, path string) bool {
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func TestDaemonTestCacheEnvironmentIsolated(t *testing.T) {
	root := filepath.Dir(t.TempDir())
	outside := filepath.Join(string(filepath.Separator), "agent-net-test-not-a-cache")
	t.Setenv("AGENT_NET_GOCACHE", "/tmp/agent-net-gocache")
	t.Setenv("GOCACHE", "/tmp/agent-net-gocache")
	t.Setenv("AGENT_NET_GOTMPDIR", "/tmp/agent-net-gotmp")
	t.Setenv("HOME", outside)
	t.Setenv("UV_CACHE_DIR", outside)

	isolateDaemonCaches(t)
	for _, key := range []string{"AGENT_NET_GOCACHE", "GOCACHE", "AGENT_NET_GOTMPDIR", "UV_CACHE_DIR"} {
		value := os.Getenv(key)
		if value == "/tmp/agent-net-gocache" || value == "/tmp/agent-net-gotmp" {
			t.Errorf("%s kept the real worker cache %q", key, value)
		}
		if !pathWithin(root, value) {
			t.Errorf("%s = %q, want a fresh folder under %s", key, value, root)
		}
	}
	if cache, goCache := os.Getenv("AGENT_NET_GOCACHE"), os.Getenv("GOCACHE"); cache != goCache {
		t.Errorf("GOCACHE = %q, want the same folder as AGENT_NET_GOCACHE %q", goCache, cache)
	}
	if home := os.Getenv("HOME"); !pathWithin(root, home) {
		t.Errorf("HOME = %q, want a folder under the test root %s", home, root)
	}

	// A HOME the test chose under its own root is kept.
	custom := t.TempDir()
	t.Setenv("HOME", custom)
	isolateDaemonCaches(t)
	if home := os.Getenv("HOME"); home != custom {
		t.Errorf("HOME = %q, want the test's own %s", home, custom)
	}
}

func startNode(t *testing.T, g *testGateway, o nodeOpts) *node {
	t.Helper()
	// Many tests call this after t.Parallel, where t.Setenv is refused. The
	// daemon gets temp cache folders through Config below, and TestMain points
	// the cache environment at temp folders once for the package.
	// TestMain enables the fake judge once for the package. Keeping that
	// environment constant lets isolated node tests run in parallel.
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
	// Most worker tests start workers as an agent with a model or harness: the
	// override check is off unless a test turns it on (worker_override_test.go).
	cfg := Config{testStopCTOOnExit: true, testNoDifficulty: true, testOpenSelection: true, GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca,
		NodeConfigDir: cfgDir, Hostname: "host-" + nc.NodeID, StateDir: seededStateDir(t), SocketPath: sock,
		HomeDir:       t.TempDir(),
		WorkerGoCache: t.TempDir(),
		WorkerGoTmp:   t.TempDir(),
		EnterSSH:      nc.EnterSSHEnabled(), EnterSSHHosts: nc.EnterSSHHosts,
		ClaudeSessionsDir: o.claudeDir, BackoffMin: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond,
		ClaudeDiscoveryGrace: 100 * time.Millisecond, PluginAckTimeout: 500 * time.Millisecond, Logger: o.logger,
		// The helper sessions live in the test's own temporary folders. A
		// test that wants the temp rule sets TempFolders itself.
		TempFolders: []string{}}
	// Files between machines: never the machine's own Tailscale or Downloads.
	cfg.Files = FileConfig{Downloads: filepath.Join(t.TempDir(), "Downloads"), TailscaleIP: func() string { return "" },
		TailscalePATH: t.TempDir(), TailscaleFallback: []string{}}
	if o.adapter != nil {
		cfg.Adapters = []Adapter{o.adapter}
	}
	cfg.DetectHarnesses = o.detect
	// A live model read in a test never runs the machine's harness. It uses
	// the same fake inventory, and a test that needs a different live list
	// sets cfg.ListModels in o.cfg.
	detect := o.detect
	cfg.ListModels = func(ctx context.Context, name string) ([]string, error) {
		if detect != nil {
			for _, h := range detect(ctx) {
				if h.Name == name {
					return append([]string(nil), h.Models...), nil
				}
			}
		}
		return nil, fmt.Errorf("%s has no live model list in this test", name)
	}
	// A test must never find a harness of the machine it runs on. A test that
	// wants the direct lookup sets HarnessSearch itself.
	cfg.HarnessSearch = &harnesses.Options{PATH: t.TempDir(), Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}
	if o.cfg != nil {
		o.cfg(&cfg)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// In these tests the "agent" and the command line are one process.
	if !o.realGate {
		d.inSession = func(int) bool { return false }
	}
	if o.exe != "" {
		d.exe = func(int) string { return o.exe }
	}
	if o.cmdline != "" {
		d.cmdline = func(int) string { return o.cmdline }
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := &node{t: t, d: d, sock: sock, adapter: o.adapter, cfg: cfg, cmdline: o.cmdline, exe: o.exe}
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
	// Other gets the method name of every push that is not a delivery.
	Other chan string
	// AutoAck confirms every push with delivered ok.
	AutoAck bool
}

func dialLocal(t *testing.T, sock string) *localClient {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	lc := &localClient{t: t, c: c, wait: map[string]chan map[string]json.RawMessage{}, Pushes: make(chan protocol.Message, 16), Other: make(chan string, 64)}
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
		if method != "deliver" {
			select {
			case lc.Other <- method:
			default:
			}
			continue
		}
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
	return lc.callWithin(scaled(20*time.Second), method, params)
}

// callWithin is call with a shorter answer budget, for a caller that polls:
// one slow reply must not use up the whole wait, so the caller can ask again.
func (lc *localClient) callWithin(budget time.Duration, method string, params any) (json.RawMessage, error) {
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
	case <-time.After(budget):
		// Capture the blocked goroutines before test cleanup tears down the
		// socket and gateway, so a rare stalled request can be diagnosed.
		var stacks bytes.Buffer
		pprof.Lookup("goroutine").WriteTo(&stacks, 2)
		return nil, fmt.Errorf("no response\ngoroutines at %s timeout:\n%s", method, stacks.String())
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
	case <-time.After(scaled(5 * time.Second)):
		lc.t.Fatal("no push received")
		return protocol.Message{}
	}
}

// nextPushOrLog waits for the next push like nextPush, but as long as the
// other waits of a worker start chain (20 s, scaled), not nextPush's 5 s. A
// start runs a worktree, a harness and several gateway round trips, which
// each slow down under host load. When nothing arrives it prints what the
// test was waiting for and the log of the node that did the work, so a rare
// miss names its cause.
func (lc *localClient) nextPushOrLog(what string, logs *logBuf) protocol.Message {
	lc.t.Helper()
	select {
	case m := <-lc.Pushes:
		return m
	case <-time.After(scaled(20 * time.Second)):
		lc.t.Fatalf("no push received while waiting for %s; node log:\n%s", what, logs.String())
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

func TestClaudeDiscoveryGrace(t *testing.T) {
	for _, tc := range []struct {
		name             string
		configured, want time.Duration
	}{
		{"default", 0, 3 * time.Second},
		{"override", 100 * time.Millisecond, 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.ClaudeDiscoveryGrace = tc.configured }})
			if got := n.d.cfg.ClaudeDiscoveryGrace; got != tc.want {
				t.Fatalf("discovery grace = %s, want %s", got, tc.want)
			}
		})
	}
}
