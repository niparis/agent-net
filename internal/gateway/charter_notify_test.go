package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/niparis/agent-net/internal/protocol"
)

// wsPair is a live websocket: server is the gateway side (what a nodeConn
// holds), client is the daemon side that reads the frames.
func wsPair(t *testing.T) (server, client *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws, err := websocket.Accept(w, r, nil); err == nil {
			accepted <- ws
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.CloseNow() })
	server = <-accepted
	t.Cleanup(func() { server.CloseNow() })
	return server, client
}

// readFrameType reads one frame from the daemon side and returns its type.
func readFrameType(t *testing.T, ws *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, raw, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var h protocol.Header
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("parse frame %s: %v", raw, err)
	}
	return h.Type
}

// A node connection with no websocket (a test fake, or one that is not wired
// yet) is a connection nobody can write to. A write says so with an error.
func TestNodeConnWriteWithoutWebsocketFails(t *testing.T) {
	nc := &nodeConn{id: "n1"}
	if err := nc.write(t.Context(), []byte(`{"type":"x"}`)); err == nil {
		t.Fatal("a write to a connection with no websocket succeeded")
	}
}

// The gateway tells every connected daemon about a charter write. A connection
// with no websocket in the table must not crash the broadcast, and the live
// connections still hear it, whichever order the table is walked in.
func TestCharterWriteSkipsConnectionsWithoutWebsocket(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	serverWS, clientWS := wsPair(t)
	gw.mu.Lock()
	for _, id := range []string{"fake-a", "fake-b", "fake-c"} {
		gw.conns[id] = &nodeConn{id: id}
	}
	gw.conns["live"] = &nodeConn{id: "live", ws: serverWS, cancel: func() {}}
	gw.mu.Unlock()
	// Close closes every websocket in the table, so the fakes leave it first.
	defer func() {
		gw.mu.Lock()
		for _, id := range []string{"fake-a", "fake-b", "fake-c"} {
			delete(gw.conns, id)
		}
		gw.mu.Unlock()
	}()

	setLevel(t, gw.store, protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "report late defects"})
	if got := readFrameType(t, clientWS); got != protocol.FrameCharterChanged {
		t.Fatalf("live connection heard %q, want %q", got, protocol.FrameCharterChanged)
	}
}

// The settings broadcast and the mcp.changed broadcast walk the same table, so
// the same fake must not crash them either.
func TestOtherBroadcastsSkipConnectionsWithoutWebsocket(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	serverWS, clientWS := wsPair(t)
	gw.mu.Lock()
	gw.conns["fake"] = &nodeConn{id: "fake"}
	gw.conns["live"] = &nodeConn{id: "live", ws: serverWS, cancel: func() {}}
	gw.mu.Unlock()
	defer func() {
		gw.mu.Lock()
		delete(gw.conns, "fake")
		gw.mu.Unlock()
	}()

	gw.settingsChanged(t.Context())
	if got := readFrameType(t, clientWS); got != "settings.sync" {
		t.Fatalf("live connection heard %q, want settings.sync", got)
	}
	gw.mcpChanged(t.Context(), "")
	if got := readFrameType(t, clientWS); got != "mcp.changed" {
		t.Fatalf("live connection heard %q, want mcp.changed", got)
	}
}

// goroutineRunning tells whether any goroutine has a frame whose function name
// contains name.
func goroutineRunning(name string) bool {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Contains(string(buf), name)
}

// The broadcast that follows a charter write belongs to the gateway: Close
// stops it and waits for it, so it never outlives the gateway or the test that
// started it.
func TestCharterBroadcastStopsWithTheGateway(t *testing.T) {
	gw, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	serverWS, clientWS := wsPair(t)
	gw.mu.Lock()
	gw.conns["live"] = &nodeConn{id: "live", ws: serverWS, cancel: func() {}}
	gw.mu.Unlock()
	setLevel(t, gw.store, protocol.CharterSetRequest{Level: "global", Attr: "rules", Value: "report late defects"})
	readFrameType(t, clientWS)
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	// Close has waited for the broadcast, so no frame of it is left. Only the
	// exit of the goroutine itself may still be in flight.
	deadline := time.Now().Add(2 * time.Second)
	for goroutineRunning("gateway.(*Gateway).charterChanged") {
		if time.Now().After(deadline) {
			t.Fatal("the charter broadcast is still running after Close")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A write after Close starts nothing.
	gw.store.charterNotify()
	if goroutineRunning("gateway.(*Gateway).charterChanged") {
		t.Fatal("a charter write after Close started a broadcast")
	}
}

// A background job gets a context that Close cancels, and Close waits for the
// job to return. A job asked for after that is not started.
func TestBackgroundJobsAreCancelledAndAwaitedOnStop(t *testing.T) {
	g := &Gateway{}
	started := make(chan struct{})
	var finished atomic.Bool
	if !g.goBackground(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		finished.Store(true)
	}) {
		t.Fatal("a job was refused before stop")
	}
	<-started
	g.stopBackground()
	if !finished.Load() {
		t.Fatal("stop returned before the job did")
	}
	var ran atomic.Bool
	if g.goBackground(func(context.Context) { ran.Store(true) }) {
		t.Fatal("a job was accepted after stop")
	}
	g.bgWG.Wait()
	if ran.Load() {
		t.Fatal("a job ran after stop")
	}
}
