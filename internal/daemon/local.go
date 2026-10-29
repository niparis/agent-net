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
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
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
		lc.d.wg.Add(1)
		go func() {
			defer lc.d.wg.Done()
			timeout := 40 * time.Second
			if strings.HasPrefix(req.Method, "mcp_") {
				timeout = protocol.MCPMaxTimeout + time.Minute // a tool call may run as long as its server allows
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
	pid, err := peerPID(lc.c)
	if err != nil {
		return nil, fmt.Errorf("cannot read the peer process: %w", err)
	}
	switch p.Client {
	case "cli":
		lc.mu.Lock()
		lc.client = "cli"
		lc.mu.Unlock()
		return map[string]any{"node_id": d.cfg.NodeID, "version": d.helloVersion(), "protocol": protocol.LocalProtocol}, nil

	case "mcp":
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
		return map[string]any{"node_id": d.cfg.NodeID, "session_id": d.currentSessionID(ls)}, nil

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
		ls := &localSession{conn: lc, info: protocol.SessionInfo{
			Harness: p.Client, NativeID: p.NativeID, PID: pid, Cwd: p.Cwd, Name: filepath.Base(p.Cwd),
			Status: status, Delivery: "plugin"}}
		k := key(p.Client, p.NativeID)
		d.mu.Lock()
		old := d.sessions[k]
		if old != nil {
			ls.sessionID = old.sessionID
		}
		d.sessions[k] = ls
		d.mu.Unlock()
		if old != nil && old.conn != nil {
			old.conn.close()
		}
		lc.bind(p.Client, ls)
		d.pushSession(ls)
		return map[string]any{"node_id": d.cfg.NodeID, "session_id": d.currentSessionID(ls)}, nil
	}
	return nil, fmt.Errorf("unknown client %q", p.Client)
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

func validStatus(s string) bool {
	return s == "idle" || s == "busy" || s == "shell" || s == "unknown"
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
	readOnly := map[string]bool{"info": true, "list": true, "get": true, "messages": true, "message_status": true,
		"nodes": true, "restart": true}
	if client == "cli" && !readOnly[method] && !strings.HasPrefix(method, "mcp_") {
		return nil, fmt.Errorf("method %q is not available to cli clients", method)
	}
	// Sender identity is never taken from params: the bound session is used.
	switch method {
	case "info":
		return map[string]any{"connected": d.Connected(), "node_id": d.cfg.NodeID,
			"gateway_url": d.cfg.GatewayURL, "sessions": d.Sessions(), "version": d.helloVersion(),
			"protocol": protocol.LocalProtocol}, nil

	case "nodes", "restart":
		return d.dispatchUpgrade(ctx, method, client)

	case "status":
		var p struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &p) != nil || !validStatus(p.Status) {
			return nil, errors.New("status must be idle, busy, shell or unknown")
		}
		if ls == nil || ls.conn != lc {
			return nil, errors.New("only plugin clients report their status")
		}
		d.mu.Lock()
		ls.info.Status = p.Status
		d.mu.Unlock()
		d.pushSession(ls)
		return nil, nil

	case "announce":
		sid, err := lc.boundID()
		if err != nil {
			return nil, err
		}
		var p protocol.ProfileAnnounce
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Name) == "" {
			return nil, errors.New("announce needs a name")
		}
		p.SessionID = sid
		return d.call(ctx, "profile.announce", p)

	case "list":
		var p protocol.QueryAgents
		if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
			return nil, errors.New("bad list params")
		}
		return d.call(ctx, "query.agents", p)

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
			MessageID      string `json:"message_id"`
			Body           string `json:"body"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if json.Unmarshal(raw, &p) != nil || p.MessageID == "" {
			return nil, errors.New("reply needs message_id and body")
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
			Type: protocol.TypeReply, Body: p.Body, ParentID: p.MessageID, IdempotencyKey: p.IdempotencyKey})

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
	return nil, fmt.Errorf("unknown method %q", method)
}
