package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

// HandoverEnv names the environment variable that carries the path of the
// handover file from the process that execs to the one that starts.
const HandoverEnv = "AGENT_NET_MCP_HANDOVER"

// handover is what the old process leaves for the new one: the two handshake
// lines the client sent once (it will not send them again), and the bytes the
// old process had read from stdin but not given to the MCP server.
type handover struct {
	V           int    `json:"v"`
	Init        []byte `json:"init,omitempty"`        // the initialize request line
	Initialized []byte `json:"initialized,omitempty"` // the notifications/initialized line
	Leftover    []byte `json:"leftover,omitempty"`
}

const listChangedLine = `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`

// stdioConn is an MCP connection over stdin and stdout that owns its framing
// (newline-delimited JSON). Because it holds the only read buffer, it knows
// exactly which input bytes are unread, so the process can replace itself with
// a newer binary without losing or repeating any byte.
type stdioConn struct {
	in  int // fd to read; 0 in production
	out io.Writer

	wmu sync.Mutex // serialises writes to out

	mu           sync.Mutex
	buf          []byte   // bytes read from in and not yet handed to the server
	pre          [][]byte // replayed handshake lines, handed out before buf
	eof          bool
	waiting      bool // the server is blocked in Read, so it holds no message
	started      bool // the replay (if any) has been fully processed
	replayHanded bool // the last replayed line has been given to the server
	announce     bool // send tools/list_changed when the replay is done
	swallow      *jsonrpc.ID

	initLine, initializedLine []byte
	pendingIn                 map[jsonrpc.ID]bool // requests the server has not answered
	pendingOut                map[jsonrpc.ID]bool // server requests the client has not answered

	execPath string                              // set when an exec is wanted
	execFn   func(h handover, path string) error // does the exec; returns only on failure
	logf     func(format string, a ...any)

	wake      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

// newStdioConn makes a connection reading fd in and writing out. If h is not
// nil, its handshake is replayed into the server first, and its leftover bytes
// come before anything read from in.
func newStdioConn(in int, out io.Writer, h *handover, logf func(string, ...any)) *stdioConn {
	c := &stdioConn{in: in, out: out, logf: logf, execFn: realExec,
		pendingIn: map[jsonrpc.ID]bool{}, pendingOut: map[jsonrpc.ID]bool{},
		wake: make(chan struct{}, 1), closed: make(chan struct{})}
	if h != nil {
		if len(h.Init) > 0 {
			c.pre = append(c.pre, h.Init)
			c.initLine = h.Init
			if m, err := jsonrpc.DecodeMessage(h.Init); err == nil {
				if r, ok := m.(*jsonrpc.Request); ok && r.IsCall() {
					id := r.ID
					c.swallow = &id
				}
			}
		}
		if len(h.Initialized) > 0 && len(h.Init) > 0 {
			c.pre = append(c.pre, h.Initialized)
			c.initializedLine = h.Initialized
			c.announce = true
		}
		c.buf = append(c.buf, h.Leftover...)
	}
	c.started = len(c.pre) == 0
	return c
}

func (c *stdioConn) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// readLoop moves input from the fd into buf. It waits with poll, and reads
// only while holding mu after poll said there is data. So no byte is ever
// held outside buf, and an exec (which holds mu) can happen at any moment:
// data that arrives later stays in the pipe for the new process.
func (c *stdioConn) readLoop() {
	chunk := make([]byte, 64*1024)
	for {
		fds := []unix.PollFd{{Fd: int32(c.in), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, -1); err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			c.setEOF()
			return
		}
		c.mu.Lock()
		n, err := syscall.Read(c.in, chunk)
		if err == syscall.EINTR || err == syscall.EAGAIN {
			c.mu.Unlock()
			continue
		}
		if n > 0 {
			c.buf = append(c.buf, chunk[:n]...)
		}
		if err != nil || n == 0 {
			c.eof = true
		}
		c.mu.Unlock()
		c.signal()
		if err != nil || n == 0 {
			return
		}
	}
}

func (c *stdioConn) setEOF() {
	c.mu.Lock()
	c.eof = true
	c.mu.Unlock()
	c.signal()
}

// nextLineLocked pops one non-empty line, or returns nil.
func (c *stdioConn) nextLineLocked() []byte {
	for {
		if len(c.pre) > 0 {
			l := c.pre[0]
			c.pre = c.pre[1:]
			if len(c.pre) == 0 {
				c.replayHanded = true
			}
			return l
		}
		var line []byte
		if i := bytes.IndexByte(c.buf, '\n'); i >= 0 {
			line = c.buf[:i]
			c.buf = c.buf[i+1:]
		} else if c.eof && len(bytes.TrimSpace(c.buf)) > 0 {
			line = c.buf
			c.buf = nil
		} else {
			return nil
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			return append([]byte(nil), line...)
		}
	}
}

// Read returns the next message. It is the only place that hands input to the
// server, and a call to it means the server is done with the previous message.
func (c *stdioConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		c.mu.Lock()
		c.waiting = true
		announce := false
		if c.replayHanded && !c.started {
			c.started = true // the server has processed the replay
			announce = c.announce
		}
		c.maybeExecLocked() // returns only if there is no exec, or it failed
		line := c.nextLineLocked()
		if line != nil {
			c.waiting = false
			c.noteInLocked(line)
		}
		eof := c.eof
		c.mu.Unlock()
		if announce {
			// The client did not ask again, so tell it the tools may differ.
			c.writeLine([]byte(listChangedLine))
		}
		if line != nil {
			msg, err := jsonrpc.DecodeMessage(line)
			if err != nil {
				return nil, fmt.Errorf("bad JSON-RPC message: %w", err)
			}
			return msg, nil
		}
		if eof {
			return nil, io.EOF
		}
		select {
		case <-c.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.closed:
			return nil, io.EOF
		}
	}
}

// noteInLocked records the handshake lines and the requests now in flight.
func (c *stdioConn) noteInLocked(line []byte) {
	var h struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(line, &h) != nil {
		return
	}
	switch {
	case h.Method == "initialize" && c.initLine == nil:
		c.initLine = line
	case h.Method == "notifications/initialized" && c.initializedLine == nil && c.initLine != nil:
		c.initializedLine = line
	}
	if len(h.ID) == 0 || string(h.ID) == "null" {
		return
	}
	var v any
	if json.Unmarshal(h.ID, &v) != nil {
		return
	}
	id, err := jsonrpc.MakeID(v)
	if err != nil {
		return
	}
	if h.Method == "" {
		delete(c.pendingOut, id) // the client answered a server request
	} else {
		c.pendingIn[id] = true
	}
}

func (c *stdioConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	c.mu.Lock()
	switch m := msg.(type) {
	case *jsonrpc.Response:
		if c.swallow != nil && m.ID == *c.swallow {
			c.swallow = nil // the answer to the replayed initialize: the client saw one already
			c.mu.Unlock()
			return nil
		}
	case *jsonrpc.Request:
		if m.IsCall() {
			c.pendingOut[m.ID] = true
		}
	}
	c.mu.Unlock()
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	err = c.writeLine(data)
	// Only now is the answer out; this may be the idle moment an exec waits for.
	c.mu.Lock()
	if m, ok := msg.(*jsonrpc.Response); ok {
		delete(c.pendingIn, m.ID)
	}
	c.maybeExecLocked()
	c.mu.Unlock()
	return err
}

func (c *stdioConn) writeLine(data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.out.Write(append(append([]byte(nil), data...), '\n'))
	return err
}

func (c *stdioConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *stdioConn) SessionID() string { return "" }

// requestExec asks to replace this process with the binary at path as soon as
// nothing is in flight. It returns at once.
func (c *stdioConn) requestExec(path string) {
	c.mu.Lock()
	if c.execPath == "" {
		c.execPath = path
	}
	c.maybeExecLocked()
	c.mu.Unlock()
}

// idleLocked: the server is waiting for input, holds no message, owes no
// answer and waits for none.
func (c *stdioConn) idleLocked() bool {
	return c.waiting && c.started && len(c.pre) == 0 && c.swallow == nil &&
		len(c.pendingIn) == 0 && len(c.pendingOut) == 0
}

// maybeExecLocked execs if one is wanted and the connection is idle. It
// returns only when it did not exec. mu is held on entry and on return, and
// stays held during the exec so the reader cannot take a byte meanwhile.
func (c *stdioConn) maybeExecLocked() {
	if c.execPath == "" || !c.idleLocked() {
		return
	}
	path := c.execPath
	c.execPath = ""
	h := handover{V: 1, Init: c.initLine, Initialized: c.initializedLine,
		Leftover: append([]byte(nil), c.buf...)}
	c.wmu.Lock() // no half-written line may be cut off by the exec
	err := c.execFn(h, path)
	c.wmu.Unlock()
	c.logf("cannot switch to the new binary, staying on this one: %v", err)
}

// ---- handover file ----

// writeHandover saves h to a private temp file and returns its path.
func writeHandover(h handover) (string, error) {
	f, err := os.CreateTemp("", "agent-net-mcp-handover-*")
	if err != nil {
		return "", err
	}
	err = json.NewEncoder(f).Encode(h)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// takeHandover reads and deletes the file named by HandoverEnv, and clears the
// variable so child processes do not see it. It returns nil if there is none.
func takeHandover(logf func(string, ...any)) *handover {
	p := os.Getenv(HandoverEnv)
	if p == "" {
		return nil
	}
	os.Unsetenv(HandoverEnv)
	raw, err := os.ReadFile(p)
	os.Remove(p)
	if err != nil {
		logf("cannot read the handover from the previous process: %v", err)
		return nil
	}
	var h handover
	if err := json.Unmarshal(raw, &h); err != nil || h.V != 1 {
		logf("unreadable handover from the previous process")
		return nil
	}
	return &h
}

// realExec replaces the process with the binary at path, keeping the pid and
// the stdin and stdout descriptors. It returns only on failure.
func realExec(h handover, path string) error {
	hp, err := writeHandover(h)
	if err != nil {
		return err
	}
	env := append(os.Environ(), HandoverEnv+"="+hp)
	err = syscall.Exec(path, os.Args, env)
	os.Remove(hp)
	return err
}

// ExePath cleans a path from os.Executable: on Linux a replaced binary reads
// back as "<path> (deleted)".
func ExePath(p string) string { return strings.TrimSuffix(p, " (deleted)") }

type stdioTransport struct{ c *stdioConn }

func (t stdioTransport) Connect(context.Context) (mcp.Connection, error) { return t.c, nil }
