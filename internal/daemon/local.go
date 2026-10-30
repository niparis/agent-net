package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// localServer serves the JSON-lines socket used by `agent-net mcp`, the Pi
// extension and the CLI.
type localServer struct {
	d    *Daemon
	ln   net.Listener
	path string

	mu    sync.Mutex
	conns map[*localConn]bool
}

func listenLocal(d *Daemon, path string) (*localServer, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Tighten a directory we own. A shared one such as /tmp stays as it is;
	// the socket itself is 0600.
	os.Chmod(dir, 0o700)
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("another daemon is already listening on %s", path)
		}
		os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &localServer{d: d, ln: ln, path: path, conns: map[*localConn]bool{}}, nil
}

func (s *localServer) serve(ctx context.Context) {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		lc := &localConn{d: s.d, c: c, srv: s, acks: map[string]chan ack{}, closed: make(chan struct{})}
		s.mu.Lock()
		s.conns[lc] = true
		s.mu.Unlock()
		s.d.wg.Add(1)
		go func() {
			defer s.d.wg.Done()
			lc.run(ctx)
		}()
	}
}

func (s *localServer) close() {
	s.ln.Close()
	os.Remove(s.path)
	s.mu.Lock()
	for lc := range s.conns {
		lc.close()
	}
	s.mu.Unlock()
}

type ack struct {
	ok     bool
	detail string
}

type localConn struct {
	d   *Daemon
	c   net.Conn
	srv *localServer

	wmu    sync.Mutex
	once   sync.Once
	closed chan struct{}

	mu     sync.Mutex
	client string
	ls     *localSession // bound session; nil for cli
	acks   map[string]chan ack
	// peerPID is the process ID of the other end, read in hello.
	peerPID int
	// tx holds the session_text requests sent to this plugin (sessiontext.go).
	tx textState
}

func (lc *localConn) close() {
	lc.once.Do(func() {
		close(lc.closed)
		lc.c.Close()
	})
}

func (lc *localConn) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	lc.wmu.Lock()
	defer lc.wmu.Unlock()
	lc.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = lc.c.Write(append(b, '\n'))
	return err
}

// push sends a message to a plugin and waits for its confirmation.
func (lc *localConn) push(ctx context.Context, m protocol.Message, timeout time.Duration) Result {
	ch := make(chan ack, 1)
	lc.mu.Lock()
	lc.acks[m.ID] = ch
	lc.mu.Unlock()
	defer func() {
		lc.mu.Lock()
		delete(lc.acks, m.ID)
		lc.mu.Unlock()
	}()
	if err := lc.writeJSON(map[string]any{"method": "deliver", "params": map[string]any{"message": m}}); err != nil {
		return Result{Detail: "plugin connection lost: " + err.Error()}
	}
	select {
	case a := <-ch:
		if a.ok {
			return Result{Delivered: true, Detail: a.detail}
		}
		return Result{Detail: "plugin refused: " + a.detail}
	case <-time.After(timeout):
		return Result{Delivered: true, Detail: "unconfirmed"}
	case <-lc.closed:
		return Result{Delivered: true, Detail: "unconfirmed"}
	case <-ctx.Done():
		return Result{Delivered: true, Detail: "unconfirmed"}
	}
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (lc *localConn) respond(id json.RawMessage, result any, err error) {
	m := map[string]any{}
	if len(id) > 0 {
		m["id"] = id
	}
	if err != nil {
		m["error"] = err.Error()
	} else {
		if result == nil {
			result = map[string]any{}
		}
		m["result"] = result
	}
	lc.writeJSON(m)
}

func (lc *localConn) run(ctx context.Context) {
	defer lc.cleanup()
	sc := bufio.NewScanner(lc.c)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // a plugin may answer session_text with a few hundred KB
	first := true
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			lc.respond(nil, nil, errors.New("bad JSON"))
			return
		}
		if first {
			first = false
			if req.Method != "hello" {
				lc.respond(req.ID, nil, errors.New("the first request must be hello"))
				return
			}
			res, err := lc.hello(req.Params)
			lc.respond(req.ID, res, err)
			if err != nil {
				return
			}
			continue
		}
		if req.Method == "delivered" {
			lc.respond(req.ID, lc.delivered(req.Params), nil)
			continue
		}
		if req.Method == "session_text_result" {
			lc.textResult(req.Params)
			lc.respond(req.ID, nil, nil)
			continue
		}
		lc.d.wg.Add(1)
		go func() {
			defer lc.d.wg.Done()
			timeout := 40 * time.Second
			if strings.HasPrefix(req.Method, "mcp_") {
				timeout = protocol.MCPMaxTimeout + time.Minute // a tool call may run as long as its server allows
			}
			if req.Method == protocol.OpWorkerStart {
				timeout = lc.d.cfg.Workers.filled().StartTimeout + 60*time.Second // the machine starts a harness
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			res, err := lc.dispatch(cctx, req.Method, req.Params)
			lc.respond(req.ID, res, err)
		}()
	}
}

func (lc *localConn) cleanup() {
	lc.close()
	lc.srv.mu.Lock()
	delete(lc.srv.conns, lc)
	lc.srv.mu.Unlock()
	lc.mu.Lock()
	ls := lc.ls
	lc.mu.Unlock()
	if ls == nil || ls.conn != lc {
		return
	}
	d := lc.d
	k := key(ls.info.Harness, ls.info.NativeID)
	d.mu.Lock()
	var sid string
	if d.sessions[k] == ls {
		delete(d.sessions, k)
		sid = ls.sessionID
	}
	d.mu.Unlock()
	if sid != "" {
		d.call(context.Background(), "session.remove", protocol.SessionRemove{SessionID: sid})
	}
}

func (lc *localConn) delivered(raw json.RawMessage) any {
	var p struct {
		MessageID string `json:"message_id"`
		OK        bool   `json:"ok"`
		Detail    string `json:"detail"`
	}
	json.Unmarshal(raw, &p)
	lc.mu.Lock()
	ch := lc.acks[p.MessageID]
	lc.mu.Unlock()
	if ch != nil {
		select {
		case ch <- ack{p.OK, p.Detail}:
		default:
		}
	}
	return nil
}

type helloParams struct {
	Client   string `json:"client"`
	Harness  string `json:"harness"`
	NativeID string `json:"native_id"`
	Cwd      string `json:"cwd"`
	Status   string `json:"status"`
	// Mode and Model are optional. A plugin says whether a person has the
	// session open (foreground, background or unknown) and which model it runs.
	Mode  string `json:"mode"`
	Model string `json:"model"`
	// Name is optional. Without it the session is named after its folder.
	Name string `json:"name"`
	// Protocol is the local protocol version of the client; Version is its
	// agent-net version (for the error message). Old clients send neither.
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
}

func (lc *localConn) hello(raw json.RawMessage) (any, error) {
	var p helloParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, errors.New("bad hello params")
	}
	d := lc.d
	if err := d.checkClientProtocol(p.Protocol, p.Version); err != nil {
		return nil, err
	}
	// The socket opens before the workers of the last run are taken back.
	// A worker's own plugin can say hello in that gap, and it must find its
	// worker.
	if d.adopted != nil {
		select {
		case <-d.adopted:
		case <-time.After(30 * time.Second):
		}
	}
	pid, err := peerPID(lc.c)
	if err != nil {
		return nil, fmt.Errorf("cannot read the peer process: %w", err)
	}
	switch p.Client {
	case "cto-mcp":
		if !d.isCTOPeer(pid) {
			return nil, errors.New("only the CTO server's process tree can use this channel")
		}
		lc.mu.Lock()
		lc.client, lc.peerPID = "cto-mcp", pid
		lc.mu.Unlock()
		return map[string]any{"version": d.helloVersion(), "protocol": protocol.LocalProtocol}, nil

	case "cli":
		lc.mu.Lock()
		lc.client = "cli"
		lc.peerPID = pid
		lc.mu.Unlock()
		return map[string]any{"node_id": d.cfg.NodeID, "version": d.helloVersion(), "protocol": protocol.LocalProtocol}, nil

	case "mcp":
		if p.Harness == "codex" {
			return lc.helloCodexMCP(p, pid)
		}
		if p.Harness != "claude" || p.NativeID == "" {
			return nil, errors.New("mcp clients must say harness claude and a native_id")
		}
		ppid, err := parentPID(pid)
		if err != nil {
			return nil, fmt.Errorf("cannot read the parent process: %w", err)
		}
		raw, err := os.ReadFile(filepath.Join(d.cfg.ClaudeSessionsDir, strconv.Itoa(ppid)+".json"))
		if err != nil {
			return nil, fmt.Errorf("peer check failed: parent process %d is not a Claude Code session", ppid)
		}
		var sf struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &sf) != nil || sf.SessionID != p.NativeID {
			return nil, errors.New("peer check failed: native_id does not match the Claude session of the parent process")
		}
		var ls *localSession
		deadline := time.Now().Add(3 * time.Second)
		for {
			d.mu.Lock()
			ls = d.sessions[key("claude", p.NativeID)]
			d.mu.Unlock()
			if ls != nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if ls == nil {
			return nil, errors.New("the daemon has not discovered this Claude session")
		}
		lc.bind("mcp", ls)
		d.noteMCPHello(p.NativeID)
		// One line per hello. After an upgrade, a session's `agent-net mcp` says
		// hello on the old version (and is told to switch), then on the new one.
		ver := p.Version
		if ver == "" {
			ver = "(unknown version)"
		}
		d.Logf("mcp hello: claude session %s pid %d runs agent-net %s, daemon runs %s", p.NativeID, pid, ver, d.helloVersion())
		return d.mcpHelloResult(ls, "claude", p.Version), nil

	case "pi", "opencode":
		// Plugin clients run inside the harness process. The OpenCode plugin
		// opens one connection per session, all from the one service process.
		if p.Harness != p.Client || p.NativeID == "" {
			return nil, fmt.Errorf("%s clients must say harness %s and a native_id", p.Client, p.Client)
		}
		if err := d.checkPluginPeer(p.Client, pid); err != nil {
			return nil, err
		}
		status := p.Status
		if status == "" {
			status = "idle"
		}
		if !validStatus(status) {
			return nil, fmt.Errorf("bad status %q", status)
		}
		if p.Mode != "" && !protocol.ValidMode(p.Mode) {
			return nil, fmt.Errorf("bad mode %q", p.Mode)
		}
		// A worker's own harness process connects as its session, and nobody
		// else may: the user's service sees the worker's sessions in the shared
		// database and would try to register them too.
		var wk *worker
		var winfo *protocol.WorkerInfo
		if wk = d.workerForPeer(pid); wk != nil {
			if err := d.bindWorkerNative(wk, p.NativeID); err != nil {
				return nil, err
			}
			winfo = d.workerInfo(wk)
			p.Mode = protocol.ModeBackground // nobody watches a worker
		} else if d.workerOwnsNative(p.Client, p.NativeID) || d.st.workerNativeKnown(p.Client, p.NativeID) {
			return nil, errors.New("that session belongs to a worker started by this daemon; only the worker's own process can connect as it")
		} else if d.inWorktreesDir(p.Cwd) {
			// A worker of an older run, whose record is gone: the native ID is
			// unknown, but the folder gives it away.
			return nil, errors.New("that session belongs to an agent-net worker; only the worker's own process can connect as it")
		}
		if p.Mode == "" {
			// A plugin from before the mode existed, or one that cannot tell
			// (OpenCode): say "unknown" and not nothing. An empty mode is
			// reserved for a machine on an older agent-net.
			p.Mode = protocol.ModeUnknown
		}
		ls := &localSession{conn: lc, info: protocol.SessionInfo{
			Harness: p.Client, NativeID: p.NativeID, PID: pid, Cwd: p.Cwd, Name: d.pluginName(p.Name, p.Cwd),
			Status: status, Delivery: "plugin", Repo: d.repo.Refresh(p.Cwd),
			Mode: p.Mode, Model: protocol.CleanModel(p.Model), Worker: winfo}}
		k := key(p.Client, p.NativeID)
		if wk != nil && wk.sessionID != "" {
			// An adopted worker comes back under the ID it had, even if the
			// gateway lost its record meanwhile.
			ls.sessionID = wk.sessionID
			ls.info.SessionID = wk.sessionID
		}
		d.mu.Lock()
		old := d.sessions[k]
		if old != nil {
			ls.sessionID = old.sessionID
			// A plugin reconnect (a reload, or a service restart) does not
			// clear a wait: the state lives in the daemon, and a session that
			// is really working again sends status busy.
			ls.info.Waiting = old.info.Waiting
		}
		d.sessions[k] = ls
		d.mu.Unlock()
		if old != nil && old.conn != nil {
			old.conn.close()
		}
		lc.bind(p.Client, ls)
		d.pushSession(ls)
		if wk != nil {
			d.workerSessionReady(wk, d.currentSessionID(ls))
		}
		return map[string]any{"node_id": d.cfg.NodeID, "session_id": d.currentSessionID(ls), "version": d.helloVersion()}, nil
	}
	return nil, fmt.Errorf("unknown client %q", p.Client)
}

// helloCodexMCP is the hello of `agent-net mcp --codex`. Codex starts one such
// process per thread, as a child of the shared app server, and tells it nothing
// about the thread. The shim learns the thread from the _meta of its first tool
// call (set by Codex, not by the model) and says hello again with it
// (decision 0012, C3). Two checks:
//
//   - the parent process is the app server that this daemon is connected to.
//     The daemon reads that process ID from the control socket (the kernel
//     knows who listens), so no process name is trusted;
//   - the thread, if named, is one this daemon discovered.
//
// A hello with no thread is allowed after the first check; it can only make
// calls that need no session.
func (lc *localConn) helloCodexMCP(p helloParams, pid int) (any, error) {
	d := lc.d
	ca, _ := d.adapters["codex"].(*codexAdapter)
	if ca == nil {
		return nil, errors.New("this daemon has no Codex support")
	}
	srv := ca.serverPID()
	if srv == 0 {
		return nil, errors.New("peer check failed: this daemon is not connected to a Codex app server")
	}
	ppid, err := parentPID(pid)
	if err != nil {
		return nil, fmt.Errorf("cannot read the parent process: %w", err)
	}
	if ppid != srv {
		return nil, errors.New("peer check failed: the parent process is not the Codex app server that this daemon is connected to")
	}
	if p.NativeID == "" {
		lc.mu.Lock()
		lc.client = "mcp"
		lc.peerPID = pid
		lc.mu.Unlock()
		return map[string]any{"node_id": d.cfg.NodeID, "version": d.helloVersion()}, nil
	}
	if !ca.isThread(p.NativeID) {
		return nil, errors.New("the daemon has not discovered this Codex thread (is it open in a window?)")
	}
	d.mu.Lock()
	ls := d.sessions[key("codex", p.NativeID)]
	d.mu.Unlock()
	if ls == nil {
		return nil, errors.New("the daemon has not discovered this Codex thread")
	}
	lc.bind("mcp", ls)
	lc.mu.Lock()
	lc.peerPID = pid
	lc.mu.Unlock()
	d.noteMCPHello(p.NativeID)
	d.Logf("mcp hello: codex thread %s pid %d runs agent-net %s, daemon runs %s", p.NativeID, pid, firstNonEmpty(p.Version, "(unknown version)"), d.helloVersion())
	return d.mcpHelloResult(ls, "codex", p.Version), nil
}

// checkPluginPeer checks that the process behind a plugin connection is the
// harness it claims. Pi: the command line mentions pi. OpenCode: the
// executable file is named opencode (the binary is opencode or opencode.exe;
// the command line is not a reliable name for it).
func (d *Daemon) checkPluginPeer(client string, pid int) error {
	switch client {
	case "pi":
		if !strings.Contains(strings.ToLower(d.cmdline(pid)), "pi") {
			return errors.New("peer check failed: the connecting process is not Pi")
		}
	case "opencode":
		exe := strings.ToLower(filepath.Base(d.exe(pid)))
		if !strings.HasPrefix(exe, "opencode") {
			return errors.New("peer check failed: the connecting process is not OpenCode")
		}
	}
	return nil
}

func validStatus(s string) bool { return protocol.ValidStatus(s) }

// pluginName is the name a plugin gave its session, cleaned (control characters
// removed, at most protocol.MaxModelLen bytes), or the project name. The
// project is the main repository, so a session in a linked worktree is not
// named after the worktree folder. Plugins name a session "<folder>" or
// "<folder>: <title>"; the folder part is swapped for the project.
func (d *Daemon) pluginName(name, cwd string) string {
	project := d.repo.Lookup(cwd).Project
	if project == "" {
		project = filepath.Base(cwd)
	}
	name = protocol.CleanModel(name)
	if name == "" {
		return project
	}
	folder := filepath.Base(cwd)
	if folder == project {
		return name
	}
	if name == folder {
		return project
	}
	if rest, ok := strings.CutPrefix(name, folder+": "); ok {
		return project + ": " + rest
	}
	return name
}

func (lc *localConn) bind(client string, ls *localSession) {
	lc.mu.Lock()
	lc.client, lc.ls = client, ls
	lc.mu.Unlock()
}

func (d *Daemon) currentSessionID(ls *localSession) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ls.sessionID
}

func (lc *localConn) boundID() (string, error) {
	lc.mu.Lock()
	ls := lc.ls
	lc.mu.Unlock()
	if ls == nil {
		return "", errors.New("this method needs a bound session")
	}
	id := lc.d.currentSessionID(ls)
	if id == "" {
		return "", errors.New("the gateway has not registered this session yet (is the gateway reachable?)")
	}
	return id, nil
}

func (lc *localConn) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	d := lc.d
	lc.mu.Lock()
	client, ls := lc.client, lc.ls
	lc.mu.Unlock()
	if client == "cto-mcp" {
		if method != "cto_tool" {
			return nil, errors.New("method not available to CTO channel")
		}
		return lc.ctoTool(ctx, raw)
	}
	if res, ok, err := lc.dispatchOperator(ctx, method, raw); ok {
		return res, err
	}
	if method == protocol.MethodHint {
		return lc.hintCall(ctx, client, ls, raw)
	}
	if method == protocol.MethodPolicy {
		return lc.policyCall(ctx, client, ls, raw)
	}
	if method == protocol.MethodWaiting {
		return lc.waitingCall(client, ls, raw)
	}
	readOnly := map[string]bool{"info": true, "list": true, "get": true, "messages": true, "message_status": true,
		"nodes": true, "restart": true, "projects": true, "events": true, protocol.OpWorkers: true}
	if client == "cli" && !readOnly[method] && !strings.HasPrefix(method, "mcp_") &&
		method != "project_add" && method != "project_remove" &&
		method != protocol.OpSend && method != protocol.OpWorkerStart && method != protocol.OpWorkerStop {
		return nil, fmt.Errorf("method %q is not available to cli clients", method)
	}
	// Sender identity is never taken from params: the bound session is used.
	switch method {
	case "info":
		return protocol.OperatorInfo{Connected: d.Connected(), NodeID: d.cfg.NodeID, Hostname: d.hostName(),
			GatewayURL: d.cfg.GatewayURL, Sessions: d.Sessions(), Version: d.helloVersion(),
			Protocol: protocol.LocalProtocol, OwnerSession: d.ownerSessionID(), Launch: !d.cfg.NoLaunch}, nil

	case "nodes", "restart":
		return d.dispatchUpgrade(ctx, method, client)

	case "status":
		// Status, mode and model are all optional; the ones present change.
		var p struct {
			Status string  `json:"status"`
			Mode   *string `json:"mode"`
			Model  *string `json:"model"`
			Name   *string `json:"name"`
		}
		if json.Unmarshal(raw, &p) != nil || (p.Status == "" && p.Mode == nil && p.Model == nil && p.Name == nil) ||
			(p.Status != "" && !validStatus(p.Status)) {
			return nil, errors.New("status must be idle, busy, shell, dormant or unknown")
		}
		if p.Mode != nil && !protocol.ValidMode(*p.Mode) {
			return nil, errors.New("mode must be foreground, background or unknown")
		}
		if ls == nil || ls.conn != lc {
			return nil, errors.New("only plugin clients report their status")
		}
		d.mu.Lock()
		if p.Status != "" {
			ls.info.Status = p.Status
			if p.Status == "busy" {
				// Working again: the user answered, or the session moved on.
				ls.info.Waiting = nil
			}
		}
		if p.Mode != nil && ls.info.Worker == nil { // a worker is always background
			ls.info.Mode = *p.Mode
		}
		if p.Model != nil {
			ls.info.Model = protocol.CleanModel(*p.Model)
		}
		if p.Name != nil {
			ls.info.Name = d.pluginName(*p.Name, ls.info.Cwd)
		}
		cwd := ls.info.Cwd
		d.mu.Unlock()
		repo := d.repo.Refresh(cwd)
		d.mu.Lock()
		ls.info.Repo = repo
		d.mu.Unlock()
		d.pushSession(ls)
		return nil, nil

	case "announce":
		sid, err := lc.boundID()
		if err != nil {
			return nil, err
		}
		var p protocol.ProfileAnnounce
		if json.Unmarshal(raw, &p) != nil || (strings.TrimSpace(p.Task) == "" && strings.TrimSpace(p.Name) == "") {
			return nil, errors.New("announce needs a task")
		}
		p.SessionID = sid
		// The function comes from the project's AGENTS.md, never from the agent.
		p.Function = ""
		res, err := d.call(ctx, "profile.announce", p)
		if err != nil {
			return nil, err
		}
		// An announce without waiting clears an earlier one: the session went
		// on to something else.
		if ls != nil {
			d.setWaiting(ls, protocol.CleanWaiting(p.Waiting))
		}
		return res, nil

	case "list":
		var p protocol.QueryAgents
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad list params")
		}
		return d.call(ctx, "query.agents", p)

	case "projects":
		var p protocol.QueryProjects
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad projects params")
		}
		return d.call(ctx, "query.projects", p)

	case "events":
		var p protocol.QueryEvents
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad events params")
		}
		return d.call(ctx, "query.events", p)

	case "get":
		var p protocol.QueryAgent
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
			return nil, errors.New("get needs session_id")
		}
		return d.call(ctx, "query.agent", p)

	case "send":
		sid, err := lc.boundID()
		if err != nil {
			return nil, err
		}
		var p struct {
			To             string `json:"to"`
			Body           string `json:"body"`
			MessageType    string `json:"message_type"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad send params")
		}
		to, err := d.resolveTo(ctx, p.To)
		if err != nil {
			return nil, err
		}
		return d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: to, Type: p.MessageType,
			Body: p.Body, IdempotencyKey: p.IdempotencyKey})

	case "reply":
		sid, err := lc.boundID()
		if err != nil {
			return nil, err
		}
		var p struct {
			protocol.ReplyOutcome
			MessageID      string `json:"message_id"`
			Body           string `json:"body"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if json.Unmarshal(raw, &p) != nil || p.MessageID == "" {
			return nil, errors.New("reply needs message_id and body")
		}
		p.ReplyOutcome, err = protocol.CleanOutcome(p.ReplyOutcome)
		if err != nil {
			return nil, err
		}
		data, err := d.call(ctx, "query.message", protocol.QueryMessage{MessageID: p.MessageID})
		if err != nil {
			return nil, err
		}
		var parent struct {
			Message protocol.Message `json:"message"`
		}
		if err := json.Unmarshal(data, &parent); err != nil {
			return nil, err
		}
		return d.send(ctx, protocol.MsgSend{Sender: sid, Recipient: parent.Message.From,
			Type: protocol.TypeReply, Body: p.Body, ParentID: p.MessageID, IdempotencyKey: p.IdempotencyKey, ReplyOutcome: p.ReplyOutcome})

	case "messages":
		var p protocol.QueryMessages
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad messages params")
		}
		if client != "cli" {
			sid, err := lc.boundID()
			if err != nil {
				return nil, err
			}
			p.SessionID = sid
		}
		return d.call(ctx, "query.messages", p)

	case "message_status":
		var p protocol.QueryMessage
		if json.Unmarshal(raw, &p) != nil || p.MessageID == "" {
			return nil, errors.New("message_status needs message_id")
		}
		return d.call(ctx, "query.message", p)
	}
	if res, ok, err := lc.dispatchMCP(ctx, client, method, raw); ok {
		return res, err
	}
	if res, ok, err := lc.dispatchProject(ctx, client, method, raw); ok {
		return res, err
	}
	if res, ok, err := lc.dispatchActions(ctx, client, method, raw); ok {
		return res, err
	}
	return nil, fmt.Errorf("unknown method %q", method)
}
