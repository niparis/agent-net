package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/protocol"
)

const envFakeCodexShim = "FAKE_CODEX_SHIM"

// fakeCodexShim is `agent-net mcp --codex`: the real server code, run by the
// test binary as a child of the test (which plays the Codex app server).
func fakeCodexShim() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := mcpserver.RunCodex(ctx, os.Getenv("AGENT_NET_SOCKET"), mcpserver.Options{Version: "dev", NoAutoUpgrade: true}); err != nil {
		os.Stderr.WriteString("shim: " + err.Error() + "\n")
	}
}

// ---- a fake shared Codex app server ----

type fakeThread struct {
	ID, Cwd, Name, Model, Status, Source string
	Ephemeral                            bool
	Parent                               string
}

type fakeQueued struct {
	Thread, ClientID, Text string
}

// fakeShared speaks the part of the app-server protocol that the adapter uses,
// on a control socket in a fake Codex home. It runs in the test process, so its
// process ID is the test's: a child of the test is a child of "the app server".
type fakeShared struct {
	t    *testing.T
	home string

	mu      sync.Mutex
	threads map[string]*fakeThread
	queued  []fakeQueued
	conns   []*websocket.Conn
	reads   int
}

func startFakeShared(t *testing.T) *fakeShared {
	t.Helper()
	f := &fakeShared{t: t, home: shortDir(t), threads: map[string]*fakeThread{}}
	dir := filepath.Join(f.home, "app-server-control")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "app-server-control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.handle)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return f
}

func (f *fakeShared) set(th fakeThread) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threads[th.ID] = &th
}

func (f *fakeShared) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.threads, id)
}

func (f *fakeShared) queue() []fakeQueued {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeQueued(nil), f.queued...)
}

func (f *fakeShared) notify(method string) {
	f.mu.Lock()
	conns := append([]*websocket.Conn(nil), f.conns...)
	f.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"method": method, "params": map[string]any{}})
	for _, c := range conns {
		c.Write(context.Background(), websocket.MessageText, b)
	}
}

func (f *fakeShared) handle(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	for {
		_, data, err := c.Read(context.Background())
		if err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &m) != nil || len(m.ID) == 0 {
			continue
		}
		reply := func(result any, errMsg string) {
			out := map[string]any{"id": m.ID}
			if errMsg != "" {
				out["error"] = map[string]any{"code": -32600, "message": errMsg}
			} else {
				out["result"] = result
			}
			b, _ := json.Marshal(out)
			c.Write(context.Background(), websocket.MessageText, b)
		}
		var p struct {
			ThreadID            string `json:"threadId"`
			ClientUserMessageID string `json:"clientUserMessageId"`
			Input               []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		json.Unmarshal(m.Params, &p)
		f.mu.Lock()
		switch m.Method {
		case "initialize":
			f.mu.Unlock()
			reply(map[string]any{"userAgent": "fake", "codexHome": f.home}, "")
			continue
		case "thread/loaded/list":
			ids := []string{}
			for id := range f.threads {
				ids = append(ids, id)
			}
			f.mu.Unlock()
			reply(map[string]any{"data": ids, "nextCursor": nil}, "")
			continue
		case "thread/read":
			th := f.threads[p.ThreadID]
			f.reads++
			f.mu.Unlock()
			if th == nil {
				reply(nil, "thread not loaded")
				continue
			}
			var src any = th.Source
			if th.Source == "subAgent" {
				src = map[string]any{"subAgent": "review"}
			}
			var parent, name any
			if th.Parent != "" {
				parent = th.Parent
			}
			if th.Name != "" {
				name = th.Name
			}
			reply(map[string]any{"thread": map[string]any{"id": th.ID, "cwd": th.Cwd, "name": name, "model": th.Model,
				"ephemeral": th.Ephemeral, "parentThreadId": parent, "source": src, "status": map[string]any{"type": th.Status}}}, "")
			continue
		case "thread/queue/add":
			if f.threads[p.ThreadID] == nil {
				f.mu.Unlock()
				reply(nil, "thread not found")
				continue
			}
			text := ""
			if len(p.Input) > 0 {
				text = p.Input[0].Text
			}
			f.queued = append(f.queued, fakeQueued{p.ThreadID, p.ClientUserMessageID, text})
			f.mu.Unlock()
			reply(map[string]any{"queuedSubmission": map[string]any{"id": "q1"}}, "")
			continue
		}
		f.mu.Unlock()
		reply(nil, "unknown method "+m.Method)
	}
}

func codexNode(t *testing.T, g *testGateway, f *fakeShared) *node {
	t.Helper()
	return startNode(t, g, nodeOpts{cmdline: "pi", cfg: func(c *Config) { c.CodexHome = f.home; c.CodexInterval = time.Second }})
}

// codexAgents lists the Codex sessions the gateway knows, by native thread.
func codexAgents(t *testing.T, lc *localClient) map[string]protocol.AgentView {
	t.Helper()
	res := lc.mustCall("list", map[string]any{"harness": "codex", "online_only": true})
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	json.Unmarshal(res, &r)
	out := map[string]protocol.AgentView{}
	for _, a := range r.Agents {
		out[a.Cwd] = a
	}
	return out
}

func TestCodexDiscovery(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t-open", Cwd: "/work/api", Name: "api work", Model: "gpt-x", Status: "idle", Source: "cli"})
	f.set(fakeThread{ID: "t-busy", Cwd: "/work/web", Status: "active", Source: "vscode"})
	f.set(fakeThread{ID: "t-exec", Cwd: "/work/ci", Status: "idle", Source: "exec"})
	f.set(fakeThread{ID: "t-eph", Cwd: "/work/x", Status: "idle", Source: "cli", Ephemeral: true})
	f.set(fakeThread{ID: "t-sub", Cwd: "/work/x", Status: "idle", Source: "subAgent"})
	f.set(fakeThread{ID: "t-child", Cwd: "/work/x", Status: "idle", Source: "cli", Parent: "t-open"})
	n := codexNode(t, g, f)
	cli := cliClient(t, n)

	eventually(t, "three open threads are sessions", func() bool { return len(codexAgents(t, cli)) == 3 })
	got := codexAgents(t, cli)
	if a := got["/work/api"]; a.Profile.Name != "api" || a.Title != "api work" || a.Status != "idle" || a.Mode != protocol.ModeUnknown || a.Model != "gpt-x" || a.Cwd != "/work/api" {
		t.Fatalf("t-open: %+v", a)
	}
	if a := got["/work/web"]; a.Profile.Name != "web" || a.Title != "" || a.Status != "busy" {
		t.Fatalf("t-busy: %+v", a)
	}
	if a := got["/work/ci"]; a.Mode != protocol.ModeBackground {
		t.Fatalf("t-exec: %+v", a)
	}
	if _, ok := got["/work/x"]; ok {
		t.Fatal("an ephemeral thread, a sub-agent and a child thread must not be sessions")
	}

	// A status change and a closed thread follow, driven by notifications.
	f.set(fakeThread{ID: "t-busy", Cwd: "/work/web", Status: "idle", Source: "vscode"})
	f.remove("t-exec")
	f.notify("thread/status/changed")
	eventually(t, "status and removal follow", func() bool {
		g := codexAgents(t, cli)
		return len(g) == 2 && g["/work/web"].Status == "idle"
	})
}

func TestCodexKeepsWorkerSession(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	n := codexNode(t, g, f)
	cli := cliClient(t, n)
	// A worker's session is registered by the worker code, not found by
	// discovery. A discovery pass must not drop it.
	ls := &localSession{owned: true, info: protocol.SessionInfo{Harness: "codex", NativeID: "w-thread", Cwd: "/work/w",
		Name: "codex worker", Status: "idle", Delivery: "codex-app-server"}}
	n.d.mu.Lock()
	n.d.sessions[key("codex", "w-thread")] = ls
	n.d.mu.Unlock()
	n.d.pushSession(ls)
	eventually(t, "worker session registered", func() bool { return codexAgents(t, cli)["/work/w"].SessionID != "" })
	f.set(fakeThread{ID: "t1", Cwd: "/work/a", Status: "idle", Source: "cli"})
	f.notify("thread/started")
	eventually(t, "discovery ran", func() bool { return len(codexAgents(t, cli)) == 2 })
	n.d.SetSessions("codex", nil)
	if _, ok := codexAgents(t, cli)["/work/w"]; !ok {
		t.Fatal("discovery dropped the worker's session")
	}
}

func TestCodexDeliveryQueuesFramedMessage(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t1", Cwd: "/work/a", Name: "alpha", Status: "idle", Source: "cli"})
	n := codexNode(t, g, f)
	pi, _ := helloPi(t, n, "np", "/work/pi", true)
	eventually(t, "thread is a session", func() bool { return n.d.SessionID("codex", "t1") != "" })
	codexID := n.d.SessionID("codex", "t1")

	m := sendResult(t, pi.mustCall("send", map[string]any{"to": codexID, "body": "please review the diff"}))
	eventually(t, "queued", func() bool { return len(f.queue()) == 1 })
	q := f.queue()[0]
	if q.Thread != "t1" || q.ClientID != m.ID {
		t.Fatalf("queue entry: %+v (message %s)", q, m.ID)
	}
	for _, want := range []string{"please review the diff", "[agent-net] message " + m.ID, "not from your user", "agent_reply"} {
		if !strings.Contains(q.Text, want) {
			t.Fatalf("frame lacks %q:\n%s", want, q.Text)
		}
	}
	if !strings.HasPrefix(q.Text, "please review the diff") {
		t.Fatalf("the body must come first:\n%s", q.Text)
	}
	eventually(t, "state delivered", func() bool { return messageState(t, pi, m.ID) == protocol.StateDelivered })

	// A thread that closed cannot take a message.
	f.remove("t1")
	f.notify("thread/closed")
	eventually(t, "thread gone", func() bool { return !n.d.adapters["codex"].(*codexAdapter).isThread("t1") })
	res := n.d.adapters["codex"].Deliver(context.Background(), protocol.SessionInfo{NativeID: "t1"}, m)
	if res.Delivered || !strings.Contains(res.Detail, "not open") {
		t.Fatalf("delivery to a closed thread: %+v", res)
	}
}

func TestCodexFrameKinds(t *testing.T) {
	base := protocol.Message{ID: "m1", From: "s1", FromName: "bob", FromHarness: "claude", ConversationID: "c1", Body: "hello\n"}
	if got := codexFrame(base); !strings.Contains(got, "conversation c1") || !strings.HasPrefix(got, "hello\n\n[agent-net]") {
		t.Fatalf("plain: %q", got)
	}
	w := base
	w.FromWorker = true
	if got := codexFrame(w); !strings.Contains(got, "model output from a worker") {
		t.Fatalf("worker: %q", got)
	}
	o := base
	o.FromHarness, o.FromWorker = protocol.OwnerHarness, true
	if got := codexFrame(o); !strings.Contains(got, "from your user in person") || strings.Contains(got, "model output") {
		t.Fatalf("owner: %q", got)
	}
	r := base
	r.Type, r.ParentID = protocol.TypeReply, "m0"
	if got := codexFrame(r); !strings.Contains(got, "No answer is needed") || !strings.Contains(got, "answering your message m0") {
		t.Fatalf("reply: %q", got)
	}
}

// ---- the MCP shim ----

// codexShim runs the real `agent-net mcp --codex` code as a child of the test.
type codexShim struct {
	t   *testing.T
	cmd *exec.Cmd
	in  *json.Encoder
	out *bufio.Scanner
	id  int
}

func startCodexShim(t *testing.T, n *node) *codexShim {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=XXX")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, helperEnv+"=") { // the shim is not the fake MCP server
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, envFakeCodexShim+"=1", "AGENT_NET_SOCKET="+n.sock)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	s := &codexShim{t: t, cmd: cmd, in: json.NewEncoder(stdin), out: bufio.NewScanner(stdout)}
	s.out.Buffer(make([]byte, 0, 1<<16), 1<<20)
	s.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "codex", "version": "0"}})
	s.in.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return s
}

// call sends one request and returns its result, or the text of the error.
func (s *codexShim) call(method string, params any) (json.RawMessage, string) {
	s.t.Helper()
	s.id++
	s.in.Encode(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	for s.out.Scan() {
		var m struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(s.out.Bytes(), &m) != nil || m.ID == nil || *m.ID != s.id {
			continue
		}
		if m.Error != nil {
			return nil, m.Error.Message
		}
		return m.Result, ""
	}
	s.t.Fatalf("the shim ended while waiting for %s", method)
	return nil, ""
}

// tool calls an agent-net tool as Codex does, with the thread in _meta.
func (s *codexShim) tool(thread, name string, args any) (text string, isErr bool) {
	s.t.Helper()
	p := map[string]any{"name": name, "arguments": args}
	if thread != "" {
		p["_meta"] = map[string]any{"threadId": thread, "sessionId": thread}
	}
	res, errMsg := s.call("tools/call", p)
	if errMsg != "" {
		return errMsg, true
	}
	var r struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(res, &r)
	for _, c := range r.Content {
		text += c.Text
	}
	return text, r.IsError
}

func TestCodexShimSendsAsItsThread(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t1", Cwd: "/work/a", Name: "alpha", Status: "idle", Source: "cli"})
	f.set(fakeThread{ID: "t2", Cwd: "/work/b", Name: "beta", Status: "idle", Source: "cli"})
	n := codexNode(t, g, f)
	pi, piID := helloPi(t, n, "np", "/work/pi", true)
	eventually(t, "threads are sessions", func() bool { return n.d.SessionID("codex", "t1") != "" && n.d.SessionID("codex", "t2") != "" })

	sh := startCodexShim(t, n)

	// Without a thread in _meta, a tool that needs a session fails.
	if txt, isErr := sh.tool("", "agent_send", map[string]any{"to": piID, "body": "x"}); !isErr || !strings.Contains(txt, "bound session") {
		t.Fatalf("send without a thread: %q %v", txt, isErr)
	}
	// With a thread, the message is from that thread's session.
	txt, isErr := sh.tool("t1", "agent_send", map[string]any{"to": piID, "body": "hello from t1"})
	if isErr {
		t.Fatalf("send: %s", txt)
	}
	got := pi.nextPush()
	if got.Body != "hello from t1" || got.From != n.d.SessionID("codex", "t1") || got.FromHarness != "codex" {
		t.Fatalf("message: %+v", got)
	}
	// The same process serves another thread as that thread.
	if txt, isErr := sh.tool("t2", "agent_send", map[string]any{"to": piID, "body": "hello from t2"}); isErr {
		t.Fatalf("send t2: %s", txt)
	}
	if got := pi.nextPush(); got.From != n.d.SessionID("codex", "t2") {
		t.Fatalf("t2 message: %+v", got)
	}
	// A thread the daemon did not discover is refused.
	if txt, isErr := sh.tool("0badbad0", "agent_send", map[string]any{"to": piID, "body": "forged"}); !isErr || !strings.Contains(txt, "not discovered") {
		t.Fatalf("unknown thread: %q %v", txt, isErr)
	}
	// agent_reply to a message that was sent to the thread comes from the thread.
	m := sendResult(t, pi.mustCall("send", map[string]any{"to": n.d.SessionID("codex", "t1"), "body": "question"}))
	if txt, isErr := sh.tool("t1", "agent_reply", map[string]any{"message_id": m.ID, "body": "answer"}); isErr {
		t.Fatalf("reply: %s", txt)
	}
	if got := pi.nextPush(); got.Body != "answer" || got.ParentID != m.ID {
		t.Fatalf("reply: %+v", got)
	}
}

func TestCodexHelloPeerChecks(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	f := startFakeShared(t)
	f.set(fakeThread{ID: "t1", Cwd: "/work/a", Status: "idle", Source: "cli"})
	n := codexNode(t, g, f)
	eventually(t, "thread is a session", func() bool { return n.d.SessionID("codex", "t1") != "" })

	// This test process is the fake app server, but a client that is not its
	// child (this process connects itself) fails the parent check.
	lc := dialLocal(t, n.sock)
	_, err := lc.call("hello", map[string]any{"client": "mcp", "harness": "codex", "native_id": "t1", "protocol": protocol.LocalProtocol})
	if err == nil || !strings.Contains(err.Error(), "parent process is not the Codex app server") {
		t.Fatalf("hello from a non-child: %v", err)
	}
	// A daemon with no Codex connection refuses as well.
	g2 := startGateway(t, t.TempDir(), "")
	n2 := startNode(t, g2, nodeOpts{})
	lc2 := dialLocal(t, n2.sock)
	if _, err := lc2.call("hello", map[string]any{"client": "mcp", "harness": "codex", "protocol": protocol.LocalProtocol}); err == nil ||
		!strings.Contains(err.Error(), "not connected to a Codex app server") {
		t.Fatalf("hello with no server: %v", err)
	}
	// The adapter knows the process behind the socket: this process.
	if got := n.d.adapters["codex"].(*codexAdapter).serverPID(); got != os.Getpid() {
		t.Fatalf("server pid %d, want %d", got, os.Getpid())
	}
}

func TestCodexDiscoveryWaitsForServer(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := shortDir(t)
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.CodexHome = home; c.CodexInterval = 50 * time.Millisecond }})
	cli := cliClient(t, n)
	if len(codexAgents(t, cli)) != 0 {
		t.Fatal("no server, no sessions")
	}
	// The server appears later, in the same home.
	f := &fakeShared{t: t, home: home, threads: map[string]*fakeThread{}}
	dir := filepath.Join(home, "app-server-control")
	os.MkdirAll(dir, 0o700)
	ln, err := net.Listen("unix", filepath.Join(dir, "app-server-control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.handle)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	f.set(fakeThread{ID: "late", Cwd: "/work/late", Status: "idle", Source: "cli"})
	eventually(t, "late thread found", func() bool { _, ok := codexAgents(t, cli)["/work/late"]; return ok })
}
