package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// RunCodex is `agent-net mcp --codex`: the MCP server of one Codex thread
// (decision 0012, C3).
//
// Codex starts this process once per thread, as a child of its shared app
// server, and does not say which thread. Every tools/call carries the thread in
// params._meta.threadId, set by Codex and not by the model. So the process
// keeps one daemon connection per thread it is called for; each connection's
// hello names its thread, and the daemon decides whether to accept it (the
// parent must be the app server it is connected to, and the thread must be a
// discovered one). Calls that arrive without a thread (listing the tools of
// registered MCP servers) use a connection that has none, and can only make
// calls that need no session.
func RunCodex(ctx context.Context, socket string, opts Options) error {
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "agent-net mcp: "+format+"\n", a...) }
	conn := newStdioConn(0, os.Stdout, takeHandover(logf), logf)
	go conn.readLoop()
	cwd, _ := os.Getwd()
	rt := &threadRouter{socket: socket, cwd: cwd, version: opts.Version, perThread: map[string]*Daemon{}}
	rt.root = rt.newDaemon("")
	defer rt.Close()
	sw := &switcher{opts: opts, conn: conn, logf: logf}
	rt.root.OnHello = func(v string) { go sw.consider(ctx, v) }
	rt.root.conn(ctx)
	srv := New(rt.Call, opts.Version, rt.takeNote)
	sw.dropNote = rt.dropNotes
	srv.AddReceivingMiddleware(threadFromMeta())
	dyn := NewDynamic(srv, rt.Call)
	ictx, icancel := context.WithTimeout(ctx, 3*time.Second)
	dyn.Refresh(ictx)
	icancel()
	go dyn.Watch(ctx, rt.root.Changed, 15*time.Second)
	go rt.root.Reconnect(ctx, 2*time.Second)
	return srv.Run(ctx, stdioTransport{conn})
}

type threadKey struct{}

// validThreadID keeps odd values out of the hello: a Codex thread ID is a UUID, and the daemon checks it anyway.
func validThreadID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// threadFromMeta puts the thread of a tool call in its context. The value is
// read from the request's _meta, which Codex sets for every MCP tool call.
func threadFromMeta() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if c, ok := req.(*mcp.CallToolRequest); ok && method == "tools/call" && c.Params != nil {
				if id, _ := c.Params.GetMeta()["threadId"].(string); validThreadID(id) {
					ctx = context.WithValue(ctx, threadKey{}, id)
				}
			}
			return next(ctx, method, req)
		}
	}
}

// threadRouter sends each call over the connection of the thread it came from.
type threadRouter struct {
	socket, cwd, version string
	root                 *Daemon // no thread

	mu        sync.Mutex
	perThread map[string]*Daemon
}

func (r *threadRouter) newDaemon(thread string) *Daemon {
	hello := map[string]any{"client": "mcp", "harness": "codex", "cwd": r.cwd, "status": "idle",
		"protocol": protocol.LocalProtocol, "version": r.version}
	if thread != "" {
		hello["native_id"] = thread
	}
	return NewDaemon(r.socket, hello)
}

func (r *threadRouter) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if id, _ := ctx.Value(threadKey{}).(string); id != "" {
		r.mu.Lock()
		d := r.perThread[id]
		if d == nil {
			d = r.newDaemon(id)
			r.perThread[id] = d
		}
		r.mu.Unlock()
		return d.Call(ctx, method, params)
	}
	return r.root.Call(ctx, method, params)
}

func (r *threadRouter) Close() {
	r.root.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.perThread {
		d.Close()
	}
}

// takeNote gives the stale-tools note of the thread the call came from, once.
func (r *threadRouter) takeNote(ctx context.Context) string {
	id, _ := ctx.Value(threadKey{}).(string)
	if id == "" {
		return ""
	}
	r.mu.Lock()
	d := r.perThread[id]
	r.mu.Unlock()
	if d == nil {
		return ""
	}
	return d.takeNote()
}

// dropNotes forgets every note: this process is about to become the new binary.
func (r *threadRouter) dropNotes() {
	r.root.dropNote()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.perThread {
		d.dropNote()
	}
}
