package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

func waitDaemonLink(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type keepaliveGateway struct {
	pings       atomic.Int32
	respond     atomic.Bool
	traffic     atomic.Bool
	pingTraffic atomic.Bool
}

func (g *keepaliveGateway) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OnPingReceived: func(context.Context, []byte) bool {
			g.pings.Add(1)
			return g.respond.Load()
		},
	})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if g.traffic.Load() && ws.Write(ctx, websocket.MessageText, []byte(`{}`)) != nil {
					return
				}
				if g.pingTraffic.Load() {
					go func() { _ = ws.Ping(ctx) }()
				}
			}
		}
	}()
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var h protocol.Header
		if json.Unmarshal(raw, &h) != nil {
			continue
		}
		var data any = map[string]any{}
		if h.Type == "session.sync" {
			data = map[string]any{"sessions": []any{}}
		}
		b, _ := protocol.EncodeResult(h.ID, data, nil)
		if ws.Write(ctx, websocket.MessageText, b) != nil {
			return
		}
	}
}

func startKeepaliveDaemon(t *testing.T, peer *keepaliveGateway) *Daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(peer.serve))
	t.Cleanup(srv.Close)
	d, err := New(Config{
		GatewayURL: srv.URL, NodeID: "n_test", Token: "test-token", Hostname: "test-host",
		StateDir: seededStateDir(t), SocketPath: filepath.Join(shortDir(t), "d.sock"),
		ClaudeSessionsDir: t.TempDir(), PiSessionsDir: t.TempDir(),
		BackoffMin: 40 * time.Millisecond, BackoffMax: 80 * time.Millisecond,
		KeepalivePingInterval: 40 * time.Millisecond, KeepaliveTimeout: 300 * time.Millisecond,
		NoAutoUpgrade: true, DetectHarnesses: func(context.Context) []protocol.HarnessInfo { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	waitDaemonLink(t, "daemon connection", d.Connected)
	return d
}

func currentLink(d *Daemon) *link {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lk
}

func TestLinkKeepaliveDaemonSilentPeerReconnects(t *testing.T) {
	peer := &keepaliveGateway{}
	d := startKeepaliveDaemon(t, peer)
	first := currentLink(d)
	start := time.Now()
	waitDaemonLink(t, "silent gateway link to close", func() bool {
		select {
		case <-first.dead:
			return true
		default:
			return false
		}
	})
	if elapsed := time.Since(start); elapsed > 600*time.Millisecond {
		t.Fatalf("silent link took %v to close", elapsed)
	}
	if peer.pings.Load() < 2 {
		t.Fatalf("daemon sent only %d pings", peer.pings.Load())
	}
	peer.respond.Store(true)
	waitDaemonLink(t, "daemon redial", func() bool {
		l := currentLink(d)
		return l != nil && l != first
	})
}

func TestLinkKeepaliveDaemonLiveTraffic(t *testing.T) {
	for _, mode := range []string{"pongs", "data without pongs", "pings without pongs"} {
		t.Run(mode, func(t *testing.T) {
			peer := &keepaliveGateway{}
			peer.respond.Store(mode == "pongs")
			peer.traffic.Store(mode == "data without pongs")
			peer.pingTraffic.Store(mode == "pings without pongs")
			d := startKeepaliveDaemon(t, peer)
			first := currentLink(d)
			waitDaemonLink(t, "many daemon pings", func() bool {
				select {
				case <-first.dead:
					return true
				default:
					return peer.pings.Load() >= 10
				}
			})
			select {
			case <-first.dead:
				t.Fatalf("live link closed after %d pings", peer.pings.Load())
			default:
			}
		})
	}
}
