package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeDaemon speaks just enough of the daemon's local protocol for `agent-net
// mcp`: hello (answered with a version), mcp_tools and list.
type fakeDaemon struct {
	ln      net.Listener
	version atomic.Value // string
	hellos  chan map[string]any

	mu    sync.Mutex
	conns []net.Conn
}

func newFakeDaemon(t *testing.T, sock, version string) *fakeDaemon {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{ln: ln, hellos: make(chan map[string]any, 64)}
	d.version.Store(version)
	t.Cleanup(func() { ln.Close(); d.dropConns() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns = append(d.conns, c)
			d.mu.Unlock()
			go d.serve(c)
		}
	}()
	return d
}

func (d *fakeDaemon) serve(c net.Conn) {
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			return
		}
		var result any = map[string]any{}
		switch req.Method {
		case "hello":
			d.hellos <- req.Params
			result = map[string]any{"node_id": "n_test", "session_id": "s_test", "version": d.version.Load()}
		case "mcp_tools":
			result = map[string]any{"servers": []any{}}
		case "list":
			result = map[string]any{"agents": []any{map[string]any{"name": "from-fake-daemon"}}}
		}
		b, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
		c.Write(append(b, '\n'))
	}
}

// restart makes every connection drop, as a daemon restart does, and sets the
// version the next hello reports.
func (d *fakeDaemon) restart(version string) {
	d.version.Store(version)
	d.dropConns()
}

func (d *fakeDaemon) dropConns() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		c.Close()
	}
	d.conns = nil
}

func (d *fakeDaemon) waitHello(t *testing.T, version string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case h := <-d.hellos:
			if h["version"] == version {
				return h
			}
		case <-deadline:
			t.Fatalf("no hello from an agent-net %s", version)
		}
	}
}

// buildAs builds this package with the given version baked in.
func buildAs(t *testing.T, out, version string) {
	t.Helper()
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goBin, "build", "-ldflags", "-X main.version="+version, "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, b)
	}
}

// mcpClient is a fake MCP client on the pipes of an `agent-net mcp` process.
type mcpClient struct {
	t     *testing.T
	in    *os.File
	lines chan string
}

func (c *mcpClient) send(line string) {
	c.t.Helper()
	if _, err := fmt.Fprintln(c.in, line); err != nil {
		c.t.Fatal(err)
	}
}

func (c *mcpClient) next() string {
	c.t.Helper()
	select {
	case l, ok := <-c.lines:
		if !ok {
			c.t.Fatal("the mcp process closed its stdout")
		}
		return l
	case <-time.After(10 * time.Second):
		c.t.Fatal("timed out waiting for the mcp process")
		return ""
	}
}

// reply reads lines until the response with this id, and returns it and the
// notifications seen on the way.
func (c *mcpClient) reply(id int) (string, []string) {
	c.t.Helper()
	var notes []string
	for {
		l := c.next()
		if strings.Contains(l, fmt.Sprintf(`"id":%d,`, id)) || strings.Contains(l, fmt.Sprintf(`"id":%d}`, id)) {
			return l, notes
		}
		notes = append(notes, l)
	}
}

type mcpProc struct {
	cmd    *exec.Cmd
	client *mcpClient
	exited chan struct{}
}

func startMCP(t *testing.T, exe, sock, tmp string, extraEnv ...string) *mcpProc {
	t.Helper()
	cmd := exec.Command(exe, "mcp")
	cmd.Env = append(os.Environ(),
		"AGENT_NET_SOCKET="+sock,
		"AGENT_NET_CONFIG_DIR="+filepath.Join(tmp, "cfg"),
		"AGENT_NET_STATE_DIR="+filepath.Join(tmp, "state"),
		"TMPDIR="+tmp,
		"CLAUDE_CODE_SESSION_ID=test-session")
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Dir = tmp
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &mcpClient{t: t, in: in.(*os.File), lines: make(chan string, 200)}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	p := &mcpProc{cmd: cmd, client: c, exited: make(chan struct{})}
	go func() { cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-p.exited
		if t.Failed() {
			t.Logf("mcp stderr:\n%s", stderr.String())
		}
	})
	return p
}

func toolNames(t *testing.T, line string) string {
	t.Helper()
	var r struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &r); err != nil || len(r.Result.Tools) == 0 {
		t.Fatalf("not a tools/list reply: %s", line)
	}
	var n []string
	for _, tl := range r.Result.Tools {
		n = append(n, tl.Name)
	}
	return strings.Join(n, ",")
}

func handshake(t *testing.T, c *mcpClient) {
	t.Helper()
	c.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fake","version":"1"}}}`)
	if l, _ := c.reply(0); !strings.Contains(l, "serverInfo") {
		t.Fatalf("initialize reply: %s", l)
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	l, _ := c.reply(1)
	toolNames(t, l)
}

func swapBinary(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	tmp := to + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, to); err != nil { // atomic, like the upgrade does
		t.Fatal(err)
	}
}

func setupSelfUpgrade(t *testing.T) (tmp, exe, newBin, sock string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("exec is not available")
	}
	tmp, err := os.MkdirTemp("", "anmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	exe = filepath.Join(tmp, "agent-net")
	newBin = filepath.Join(tmp, "agent-net-new")
	buildAs(t, exe, "v0.0.1")
	buildAs(t, newBin, "v0.0.2")
	return tmp, exe, newBin, filepath.Join(tmp, "d.sock")
}

func TestMCPSwitchesToNewerBinary(t *testing.T) {
	tmp, exe, newBin, sock := setupSelfUpgrade(t)
	d := newFakeDaemon(t, sock, "v0.0.1")
	p := startMCP(t, exe, sock, tmp)
	pid := p.cmd.Process.Pid
	d.waitHello(t, "v0.0.1")
	handshake(t, p.client)
	p.client.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	before, _ := p.client.reply(2)

	// What `agent-net upgrade` does: replace the file atomically, restart the daemon.
	swapBinary(t, newBin, exe)
	d.restart("v0.0.2")

	// The next tool call makes the process dial the restarted daemon. It is
	// answered, then the process becomes v0.0.2 and says the tools changed.
	p.client.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent_list","arguments":{}}}`)
	if l, _ := p.client.reply(3); !strings.Contains(l, "from-fake-daemon") {
		t.Fatalf("tools/call before the switch: %s", l)
	}
	h := d.waitHello(t, "v0.0.2")
	if h["native_id"] != "test-session" {
		t.Fatalf("hello after the switch: %v", h)
	}
	var note string
	for note == "" {
		l := p.client.next()
		if strings.Contains(l, "notifications/tools/list_changed") {
			note = l
		} else {
			t.Fatalf("unexpected line while waiting for list_changed: %s", l)
		}
	}

	// Same process, same pipes: the session still works and was not re-initialized.
	p.client.send(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	l, _ := p.client.reply(4)
	if got := toolNames(t, l); got != toolNames(t, before) {
		t.Fatalf("tools after the switch %q, before %q", got, toolNames(t, before))
	}
	p.client.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"agent_list","arguments":{}}}`)
	if l, _ := p.client.reply(5); !strings.Contains(l, "from-fake-daemon") {
		t.Fatalf("tools/call after the switch: %s", l)
	}
	select {
	case <-p.exited:
		t.Fatal("the process exited")
	default:
	}
	if p.cmd.Process.Pid != pid {
		t.Fatal("pid changed") // cannot happen with exec; documents the claim
	}
	if runtime.GOOS == "linux" {
		got, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil || got != exe {
			t.Fatalf("/proc/%d/exe = %q (%v), want %q (the new file, not a deleted one)", pid, got, err, exe)
		}
	}
	// Signals still work after exec.
	p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not stop the switched process")
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "agent-net-mcp-handover-*")); len(left) != 0 {
		t.Fatalf("handover file left behind: %v", left)
	}
}

func TestMCPStaysWhenAutoUpgradeIsOff(t *testing.T) {
	tmp, exe, newBin, sock := setupSelfUpgrade(t)
	d := newFakeDaemon(t, sock, "v0.0.1")
	p := startMCP(t, exe, sock, tmp, "AGENT_NET_NO_AUTO_UPGRADE=1")
	d.waitHello(t, "v0.0.1")
	handshake(t, p.client)
	swapBinary(t, newBin, exe)
	d.restart("v0.0.2")
	p.client.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent_list","arguments":{}}}`)
	p.client.reply(3)
	// The old code redialled (hello still says v0.0.1) and nothing else happens.
	if h := d.waitHello(t, "v0.0.1"); h["native_id"] != "test-session" {
		t.Fatal(h)
	}
	select {
	case l := <-p.client.lines:
		t.Fatalf("unexpected output: %s", l)
	case <-time.After(500 * time.Millisecond):
	}
	p.client.send(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	l, _ := p.client.reply(4)
	toolNames(t, l)
}
