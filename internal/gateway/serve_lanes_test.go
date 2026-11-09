package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// serveTestPeer is a fake daemon: it answers pings, collects the result frames
// by ID, and can send one frame.
type serveTestPeer struct {
	t       *testing.T
	ws      *websocket.Conn
	mu      sync.Mutex
	results map[string]protocol.Header
	closed  chan struct{} // closed when the gateway ends the link
}

func newServeTestPeer(t *testing.T, url, token string, id string, g *Gateway) *serveTestPeer {
	t.Helper()
	var pings atomic.Int32
	ws := dialLinkPeer(t, url, token, true, &pings)
	// dialLinkPeer reads and drops every frame, so read here instead.
	ws.CloseNow()
	ws2, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws2.CloseNow() })
	p := &serveTestPeer{t: t, ws: ws2, results: map[string]protocol.Header{}, closed: make(chan struct{})}
	go func() {
		for {
			_, raw, err := ws2.Read(context.Background())
			if err != nil {
				close(p.closed)
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) == nil && h.Type == "result" {
				p.mu.Lock()
				p.results[h.ID] = h
				p.mu.Unlock()
			}
		}
	}()
	waitLink(t, "link registered", func() bool { return nodeConnected(g, id) })
	return p
}

func (p *serveTestPeer) send(v any) {
	p.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := p.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		p.t.Fatal(err)
	}
}

// waitResult waits for the result frame with this ID, whatever order the
// results arrive in.
func (p *serveTestPeer) waitResult(id string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		h, ok := p.results[id]
		p.mu.Unlock()
		if ok {
			if h.Error != "" {
				p.t.Logf("result %s: %s", id, h.Error)
			}
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// A request that waits for the database must not stop the gateway from reading
// its node. While the read loop waits, pings and pongs stop, and the link dies
// after the keepalive timeout: the node reconnects and syncs again, which adds
// load to a gateway that was already slow.
func TestServeKeepsReadingWhileRequestsWaitForTheDatabase(t *testing.T) {
	g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
	p := newServeTestPeer(t, url, token, id, g)
	p.send(map[string]any{"type": "hello", "id": "h1", "hostname": "test-host"})
	if !p.waitResult("h1", time.Second) {
		t.Fatal("no answer to hello")
	}
	// Hold every database connection, the writer and the read pool, so every
	// request that needs one waits.
	release := holdAllConnections(t, g.store)
	defer release()
	p.send(map[string]any{"type": "query.agents", "id": "q1"})
	p.send(map[string]any{"type": "hidden.set", "id": "w1", "sessions": []string{"s_x"}})
	// Three keepalive timeouts pass while both requests wait.
	select {
	case <-p.closed:
		t.Fatal("the gateway ended the link while requests waited for the database")
	case <-time.After(3 * testLinkTimeout):
	}
	release()
	if !p.waitResult("q1", time.Second) {
		t.Fatal("the read request was not answered after the database came back")
	}
	if !p.waitResult("w1", time.Second) {
		t.Fatal("the write request was not answered after the database came back")
	}
}

// Writes from one node keep their order. Fifty hidden.set frames each replace
// the stored value, so the last frame must win.
func TestServeLaneKeepsWriteOrder(t *testing.T) {
	g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
	p := newServeTestPeer(t, url, token, id, g)
	p.send(map[string]any{"type": "hello", "id": "h1", "hostname": "test-host"})
	if !p.waitResult("h1", time.Second) {
		t.Fatal("no answer to hello")
	}
	const n = 50
	for i := 0; i < n; i++ {
		p.send(map[string]any{"type": "hidden.set", "id": fmt.Sprintf("w%d", i), "sessions": []string{fmt.Sprintf("s_%d", i)}})
	}
	if !p.waitResult(fmt.Sprintf("w%d", n-1), 2*time.Second) {
		t.Fatal("the last write was not answered")
	}
	// HiddenItems drops sessions that do not exist, so read the stored value.
	var stored string
	if err := g.store.db.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, hiddenItemsKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`{"sessions":["s_%d"]}`, n-1); stored != want {
		t.Fatalf("writes ran out of order, the stored value is %s, want %s", stored, want)
	}
}

// A node that writes and then reads must see its write. The write frame runs in
// the lane and is answered when it has committed; a read after the answer runs
// on a read pool connection and has to see the row, whatever the readers did
// before.
func TestLanePoolReadSeesAWriteOnceItIsAnswered(t *testing.T) {
	g, url, id, token := newLinkGateway(t, 70*time.Millisecond)
	p := newServeTestPeer(t, url, token, id, g)
	p.send(map[string]any{"type": "hello", "id": "h1", "hostname": "test-host"})
	if !p.waitResult("h1", time.Second) {
		t.Fatal("no answer to hello")
	}
	if g.store.db.r.Load() == nil {
		t.Skip("the read pool is off")
	}
	for i := 0; i < 20; i++ {
		// Read frames in flight, so pool connections are busy and have loaded the
		// cache before the write.
		p.send(map[string]any{"type": "settings.snapshot", "id": fmt.Sprintf("r%d", i)})
		wid := fmt.Sprintf("w%d", i)
		p.send(map[string]any{"type": "hidden.set", "id": wid, "sessions": []string{fmt.Sprintf("s_%d", i)}})
		if !p.waitResult(wid, 2*time.Second) {
			t.Fatalf("write %d was not answered", i)
		}
		var stored string
		if err := g.store.db.QueryRow(`SELECT value FROM owner_settings WHERE key=?`, hiddenItemsKey).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf(`{"sessions":["s_%d"]}`, i); stored != want {
			t.Fatalf("after write %d was answered, a read gave %s, want %s", i, stored, want)
		}
	}
}
