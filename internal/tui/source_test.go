package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeDaemon speaks the daemon's local protocol (JSON lines) on a Unix socket.
type fakeDaemon struct {
	t    *testing.T
	path string
	ln   net.Listener

	mu      sync.Mutex
	methods []string
	handler func(method string, params json.RawMessage) (any, string)
	conns   []net.Conn
}

func startFakeDaemon(t *testing.T, path string, h func(method string, params json.RawMessage) (any, string)) *fakeDaemon {
	t.Helper()
	if path == "" {
		dir, err := os.MkdirTemp("", "an")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		path = filepath.Join(dir, "d.sock")
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{t: t, path: path, ln: ln, handler: h}
	t.Cleanup(d.stop)
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
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			return
		}
		d.mu.Lock()
		d.methods = append(d.methods, req.Method)
		d.mu.Unlock()
		res, errText := d.handler(req.Method, req.Params)
		resp := map[string]any{"id": req.ID}
		if errText != "" {
			resp["error"] = errText
		} else {
			resp["result"] = res
		}
		b, _ := json.Marshal(resp)
		c.Write(append(b, '\n'))
	}
}

func (d *fakeDaemon) stop() {
	d.ln.Close()
	d.mu.Lock()
	for _, c := range d.conns {
		c.Close()
	}
	d.conns = nil
	d.mu.Unlock()
	os.Remove(d.path)
}

func (d *fakeDaemon) called() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.methods...)
}

func normalHandler(method string, params json.RawMessage) (any, string) {
	switch method {
	case "hello":
		return map[string]any{"node_id": "n_here"}, ""
	case "info":
		return map[string]any{"connected": true, "node_id": "n_here", "version": "v9.9.9"}, ""
	case "nodes":
		return protocol.NodesInfo{Nodes: []protocol.NodeView{{NodeID: "n_here", Hostname: "here", Online: true}}}, ""
	case "list":
		return map[string]any{"agents": []protocol.AgentView{{SessionID: "s_1", NodeID: "n_here", Hostname: "here", Online: true,
			Mode: protocol.ModeForeground, Model: "m"}}}, ""
	case "events":
		var p protocol.QueryEvents
		json.Unmarshal(params, &p)
		return map[string]any{"events": []protocol.FeedEvent{{Event: protocol.Event{ID: p.AfterID + 1, Kind: "msg.stored", MessageID: "m_1"}}}}, ""
	case "message_status":
		return map[string]any{"message": protocol.Message{ID: "m_1", Body: "hello"}}, ""
	}
	return nil, "unknown method " + method
}

func TestDaemonSourceReads(t *testing.T) {
	d := startFakeDaemon(t, "", normalHandler)
	src := NewDaemonSource(d.path)
	defer src.Close()
	ctx := context.Background()

	snap, err := src.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Connected || snap.NodeID != "n_here" || snap.Version != "v9.9.9" || len(snap.Nodes) != 1 ||
		len(snap.Agents) != 1 || snap.Agents[0].Mode != protocol.ModeForeground || snap.Agents[0].Model != "m" {
		t.Fatalf("snapshot %+v", snap)
	}
	evs, err := src.Events(ctx, 41, 10)
	if err != nil || len(evs) != 1 || evs[0].ID != 42 {
		t.Fatalf("events %+v %v", evs, err)
	}
	msg, err := src.Message(ctx, "m_1")
	if err != nil || msg.Body != "hello" {
		t.Fatalf("message %+v %v", msg, err)
	}
	// Read-only: these are the only methods the screen ever calls, and it says
	// it is a CLI client (which the daemon limits to read-only calls).
	allowed := map[string]bool{"hello": true, "info": true, "nodes": true, "list": true, "events": true, "message_status": true}
	for _, m := range d.called() {
		if !allowed[m] {
			t.Errorf("the screen called %q", m)
		}
	}
}

func TestDaemonSourceSaysItIsACLIClient(t *testing.T) {
	var hello map[string]any
	d := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
		if method == "hello" {
			json.Unmarshal(params, &hello)
		}
		return normalHandler(method, params)
	})
	src := NewDaemonSource(d.path)
	defer src.Close()
	if _, err := src.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hello["client"] != "cli" {
		t.Fatalf("hello: %v", hello)
	}
}

func TestDaemonSourceDaemonNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none.sock")
	src := NewDaemonSource(path)
	_, err := src.Snapshot(context.Background())
	if !errors.Is(err, ErrDaemonDown) || ClassifyError(err) != ProblemDaemon || !strings.Contains(err.Error(), path) {
		t.Fatalf("got %v", err)
	}
}

func TestDaemonSourceReconnectsAfterARestart(t *testing.T) {
	dir, _ := os.MkdirTemp("", "an")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "d.sock")
	d := startFakeDaemon(t, path, normalHandler)
	src := NewDaemonSource(path)
	defer src.Close()
	ctx := context.Background()
	if _, err := src.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	d.stop() // the daemon goes away, with the connection
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := src.Snapshot(ctx)
		if errors.Is(err, ErrDaemonDown) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a stopped daemon was not noticed, last result %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	startFakeDaemon(t, path, normalHandler) // and comes back
	snap, err := src.Snapshot(ctx)
	if err != nil || !snap.Connected {
		t.Fatalf("no reconnect: %+v %v", snap, err)
	}
}

func TestDaemonSourceGatewayOffline(t *testing.T) {
	// The daemon says it has no link: no further calls.
	d := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
		if method == "info" {
			return map[string]any{"connected": false, "node_id": "n_here"}, ""
		}
		return normalHandler(method, params)
	})
	src := NewDaemonSource(d.path)
	defer src.Close()
	snap, err := src.Snapshot(context.Background())
	if err != nil || snap.Connected {
		t.Fatalf("got %+v %v", snap, err)
	}
	for _, m := range d.called() {
		if m == "list" || m == "nodes" {
			t.Errorf("called %q while the gateway is offline", m)
		}
	}

	// The link drops between info and list.
	d2 := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
		if method == "list" {
			return nil, "not connected to the gateway"
		}
		return normalHandler(method, params)
	})
	src2 := NewDaemonSource(d2.path)
	defer src2.Close()
	_, err = src2.Snapshot(context.Background())
	if !errors.Is(err, ErrGatewayDown) || ClassifyError(err) != ProblemGateway {
		t.Fatalf("got %v", err)
	}
}

func TestDaemonSourceWithoutTheNodeList(t *testing.T) {
	// A gateway that cannot list nodes still gives the sessions.
	d := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
		if method == "nodes" {
			return nil, `unknown frame type "query.nodes"`
		}
		return normalHandler(method, params)
	})
	src := NewDaemonSource(d.path)
	defer src.Close()
	snap, err := src.Snapshot(context.Background())
	if err != nil || len(snap.Agents) != 1 || len(snap.Nodes) != 0 {
		t.Fatalf("got %+v %v", snap, err)
	}
}

func TestDaemonSourceOlderPeersHaveNoFeed(t *testing.T) {
	for _, text := range []string{
		`method "events" is not available to cli clients`, // an older daemon
		`unknown method "events"`,
		`unknown frame type "query.events"`, // an older gateway
	} {
		d := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
			if method == "events" {
				return nil, text
			}
			return normalHandler(method, params)
		})
		src := NewDaemonSource(d.path)
		_, err := src.Events(context.Background(), 0, 10)
		if !errors.Is(err, ErrFeedUnavailable) {
			t.Errorf("%q: got %v", text, err)
		}
		src.Close()
	}
	// Any other refusal is a plain error.
	d := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
		if method == "events" {
			return nil, "database is locked"
		}
		return normalHandler(method, params)
	})
	src := NewDaemonSource(d.path)
	defer src.Close()
	_, err := src.Events(context.Background(), 0, 10)
	var re client.RemoteError
	if errors.Is(err, ErrFeedUnavailable) || !errors.As(err, &re) {
		t.Fatalf("got %v", err)
	}
}

func TestIsOldPeer(t *testing.T) {
	if isOldPeer("database is locked") || !isOldPeer(`unknown frame type "x"`) {
		t.Fatal("isOldPeer")
	}
}
