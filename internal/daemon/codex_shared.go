package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

// codexRPC is a JSON-RPC connection to the shared Codex app server, over its
// control socket (decision 0011). The socket speaks WebSocket at ws://localhost/
// and has no token: it is a 0600 socket in a 0700 folder.
//
// This client only reads and queues. It never starts or resumes a thread, so it
// is never a subscriber: it gets no turn events and no approval requests, and it
// does not keep a thread loaded.
type codexRPC struct {
	c    *websocket.Conn
	pid  int // the app server process, from the socket (0 if unknown)
	note func(method string, params json.RawMessage)

	wmu sync.Mutex // writes

	mu      sync.Mutex
	next    int
	pending map[int]chan codexAnswer

	dead chan struct{}
	once sync.Once
}

// codexSocketPath is the control socket of the shared app server in a Codex
// home folder.
func codexSocketPath(home string) string {
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

// dialCodexSocket connects to the control socket and says initialize. The path
// is a symlink to a short path in /tmp (a socket path is limited to about 108
// bytes), so the link is followed first. The target must be a socket of ours.
func dialCodexSocket(ctx context.Context, sock, version string, note func(string, json.RawMessage)) (*codexRPC, error) {
	target, err := filepath.EvalSymlinks(sock)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("%s is not a socket", target)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return nil, fmt.Errorf("%s is owned by uid %d", target, st.Uid)
	}
	var pidv atomic.Int64
	hc := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, "unix", target)
			if err == nil {
				// The process that listens on the socket is the app server.
				if p, err := peerPID(c); err == nil {
					pidv.Store(int64(p))
				}
			}
			return c, err
		}}}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(32 << 20)
	r := &codexRPC{c: c, pid: int(pidv.Load()), note: note, pending: map[int]chan codexAnswer{}, dead: make(chan struct{})}
	go r.readLoop()
	ictx, icancel := context.WithTimeout(ctx, 10*time.Second)
	defer icancel()
	// Experimental API on: the queue methods are experimental.
	if _, err := r.call(ictx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "agent-net", "title": nil, "version": version},
		"capabilities": map[string]any{"experimentalApi": true}}); err != nil {
		r.close()
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	if err := r.write(map[string]any{"method": "initialized"}); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func (r *codexRPC) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.c.Write(ctx, websocket.MessageText, b)
}

func (r *codexRPC) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan codexAnswer, 1)
	r.mu.Lock()
	r.next++
	id := r.next
	r.pending[id] = ch
	r.mu.Unlock()
	if err := r.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
		return nil, err
	}
	select {
	case a := <-ch:
		return a.result, a.err
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
		return nil, ctx.Err()
	case <-r.dead:
		return nil, errors.New("the connection to the Codex app server ended")
	}
}

func (r *codexRPC) close() {
	r.once.Do(func() { close(r.dead) })
	r.c.Close(websocket.StatusNormalClosure, "")
}

func (r *codexRPC) readLoop() {
	defer r.once.Do(func() { close(r.dead) })
	for {
		_, data, err := r.c.Read(context.Background())
		if err != nil {
			return
		}
		var m codexMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			// A request from the server. A client that never subscribes should get
			// none; if one comes, it is not ours to answer.
			r.write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "agent-net does not answer " + m.Method}})
		case m.Method != "":
			if r.note != nil {
				r.note(m.Method, m.Params)
			}
		case len(m.ID) > 0:
			var id int
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			r.mu.Lock()
			ch := r.pending[id]
			delete(r.pending, id)
			r.mu.Unlock()
			if ch == nil {
				continue
			}
			if m.Error != nil {
				ch <- codexAnswer{err: errors.New(m.Error.Message)}
			} else {
				ch <- codexAnswer{result: m.Result}
			}
		}
	}
}
