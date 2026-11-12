package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/linkkeepalive"
	"github.com/niparis/agent-net/internal/phrasing"
	"github.com/niparis/agent-net/internal/protocol"
)

const testLinkPing = 40 * time.Millisecond
const testLinkTimeout = 300 * time.Millisecond

func waitLink(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newLinkGateway(t *testing.T, grace time.Duration) (*Gateway, string, string, string) {
	t.Helper()
	return newLinkGatewayAt(t, grace, testLinkPing, testLinkTimeout)
}

// newQuietLinkGateway is newLinkGateway with the production keepalive timings.
// A test that is not about the keepalive must not lose its link when a loaded
// machine takes longer than the short test timeout to answer a ping.
func newQuietLinkGateway(t *testing.T, grace time.Duration) (*Gateway, string, string, string) {
	t.Helper()
	return newLinkGatewayAt(t, grace, linkkeepalive.DefaultPingInterval, linkkeepalive.DefaultTimeout)
}

func newLinkGatewayAt(t *testing.T, grace, ping, timeout time.Duration) (*Gateway, string, string, string) {
	t.Helper()
	g, err := New(Options{DataDir: seededDataDir(t), OfflineGrace: grace,
		KeepalivePingInterval: ping, KeepaliveTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	id, token, err := g.store.CreateNode("test-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(g.Handler())
	t.Cleanup(srv.Close)
	return g, "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/node", id, token
}

func dialLinkPeer(t *testing.T, url, token string, respond bool, pings *atomic.Int32) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
		OnPingReceived: func(context.Context, []byte) bool {
			pings.Add(1)
			return respond
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	go func() {
		for {
			if _, _, err := ws.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	return ws
}

func sendLinkSession(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	for _, v := range []any{
		map[string]any{"type": "hello", "id": "1", "hostname": "test-host"},
		map[string]any{"type": "session.sync", "id": "2", "sessions": []protocol.SessionInfo{{Harness: "fake", NativeID: "session", Name: "test-session"}}},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.Write(context.Background(), websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
}

func nodeConnected(g *Gateway, id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conns[id] != nil
}

func sessionOnline(g *Gateway, nodeID string) bool {
	rows, err := g.store.Agents(protocol.QueryAgents{})
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.NodeID == nodeID && row.Online {
			return true
		}
	}
	return false
}

func TestLinkKeepaliveGatewayOfflineAndRedial(t *testing.T) {
	g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
	var pings atomic.Int32
	ws := dialLinkPeer(t, url, token, false, &pings)
	sendLinkSession(t, ws)
	waitLink(t, "session online", func() bool { return sessionOnline(g, id) })
	start := time.Now()
	waitLink(t, "silent node link to close", func() bool { return !nodeConnected(g, id) })
	if elapsed := time.Since(start); elapsed > 2*testLinkTimeout {
		t.Fatalf("silent link took %v to close", elapsed)
	}
	if pings.Load() < 2 {
		t.Fatalf("gateway sent only %d pings", pings.Load())
	}
	waitLink(t, "session offline", func() bool { return !sessionOnline(g, id) })
	ws = dialLinkPeer(t, url, token, true, &pings)
	sendLinkSession(t, ws)
	waitLink(t, "node and session online after redial", func() bool {
		return nodeConnected(g, id) && sessionOnline(g, id)
	})
}

func TestLinkKeepaliveGatewayLiveTraffic(t *testing.T) {
	for _, mode := range []string{"pongs", "data without pongs", "pings without pongs"} {
		t.Run(mode, func(t *testing.T) {
			g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
			var pings atomic.Int32
			ws := dialLinkPeer(t, url, token, mode == "pongs", &pings)
			// Dial can return before the gateway registers the link.
			waitLink(t, "link registered", func() bool { return nodeConnected(g, id) })
			if mode != "pongs" {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() {
					ticker := time.NewTicker(testLinkPing)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							if mode == "pings without pongs" {
								go func() { _ = ws.Ping(ctx) }()
							} else if ws.Write(ctx, websocket.MessageText, []byte(`{}`)) != nil {
								return
							}
						}
					}
				}()
			}
			waitLink(t, "many gateway pings", func() bool { return pings.Load() >= 10 || !nodeConnected(g, id) })
			if !nodeConnected(g, id) || pings.Load() < 10 {
				t.Fatalf("live link closed after %d pings", pings.Load())
			}
		})
	}
}

func TestLinkKeepaliveGatewayEmptyFragments(t *testing.T) {
	g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
	var pings atomic.Int32
	ws := dialLinkPeer(t, url, token, false, &pings)
	waitLink(t, "link registered", func() bool { return nodeConnected(g, id) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan struct{})
	var count atomic.Int32
	go func() {
		writer, err := ws.Writer(ctx, websocket.MessageText)
		if err != nil {
			return
		}
		ticker := time.NewTicker(testLinkPing)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				// Non-final frames are buffered by the library. Send enough
				// empty frames to flush the writer on every tick.
				for range 2200 {
					if _, err := writer.Write(nil); err != nil {
						return
					}
				}
				count.Add(1)
			}
		}
	}()
	waitLink(t, "empty continuation frames", func() bool { return count.Load() >= 12 || !nodeConnected(g, id) })
	if !nodeConnected(g, id) || count.Load() < 12 {
		t.Fatalf("link closed after %d empty fragments", count.Load())
	}
	close(stop)
	waitLink(t, "link to close after fragments stop", func() bool { return !nodeConnected(g, id) })
}

func TestLinkOfflineGraceQuickRedial(t *testing.T) {
	g, url, id, token := newLinkGateway(t, time.Hour)
	var pings atomic.Int32
	ws := dialLinkPeer(t, url, token, true, &pings)
	sendLinkSession(t, ws)
	waitLink(t, "session online", func() bool { return sessionOnline(g, id) })
	ws.CloseNow()
	waitLink(t, "node disconnected", func() bool { return !nodeConnected(g, id) })
	// Capture the timer before reconnecting. Its callback may already be queued
	// when Stop runs, so exercise that callback explicitly after the redial.
	g.mu.Lock()
	oldTimer := g.timers[id]
	g.mu.Unlock()
	if oldTimer == nil {
		t.Fatal("disconnect did not install a grace timer")
	}
	ws = dialLinkPeer(t, url, token, true, &pings)
	waitLink(t, "node reconnected", func() bool { return nodeConnected(g, id) })
	// No new session sync should be needed to preserve the online session.
	g.nodeGone(id, oldTimer)
	if !sessionOnline(g, id) {
		t.Fatal("quick redial let the old timer mark the session offline")
	}
}

func TestLinkKeepaliveGatewayBusyHandler(t *testing.T) {
	g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
	fake := &blockingPhraseWorker{started: make(chan struct{}), release: make(chan struct{})}
	g.store.phraseCleaner = phrasing.New(fake)
	t.Cleanup(func() { close(fake.release) })
	owner, err := g.store.UpsertSession(id, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner", Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	var pings atomic.Int32
	ws := dialLinkPeer(t, url, token, true, &pings)
	request := map[string]string{"type": "tools.summary", "id": "1", "owner": owner, "a_hash": "a", "b_hash": "b", "a": "first", "b": "second"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(context.Background(), websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	waitLink(t, "long request to start", func() bool {
		select {
		case <-fake.started:
			return true
		default:
			return false
		}
	})
	waitLink(t, "pings during the request", func() bool { return pings.Load() >= 10 || !nodeConnected(g, id) })
	if !nodeConnected(g, id) || pings.Load() < 10 {
		t.Fatalf("busy handler closed a responsive link after %d pings", pings.Load())
	}
}

func TestLinkOfflineGraceDefaults(t *testing.T) {
	g, err := New(Options{DataDir: seededDataDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.opts.OfflineGrace <= 0 || g.opts.OfflineGrace > 10*time.Second {
		t.Fatalf("offline grace = %v, want at most 10s", g.opts.OfflineGrace)
	}
	if g.opts.KeepalivePingInterval != 20*time.Second || g.opts.KeepaliveTimeout != 45*time.Second {
		t.Fatalf("keepalive defaults = %v, %v", g.opts.KeepalivePingInterval, g.opts.KeepaliveTimeout)
	}
}

func TestLinkOfflineGraceStaleTimer(t *testing.T) {
	g, _, id, _ := newLinkGateway(t, 70*time.Millisecond)
	_, err := g.store.UpsertSession(id, protocol.SessionInfo{Harness: "fake", NativeID: "session", Name: "test-session"})
	if err != nil {
		t.Fatal(err)
	}
	old := &offlineTimer{}
	current := &offlineTimer{timer: time.NewTimer(time.Hour)}
	defer current.timer.Stop()
	g.mu.Lock()
	g.timers[id] = current
	g.mu.Unlock()
	g.nodeGone(id, old)
	g.mu.Lock()
	kept := g.timers[id] == current
	g.mu.Unlock()
	if !kept || !sessionOnline(g, id) {
		t.Fatal("an expired old timer changed a newer disconnect's grace period")
	}
}
