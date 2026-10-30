package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// link is one WebSocket connection to the gateway.
type link struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	dead chan struct{}

	mu      sync.Mutex
	nextID  int
	pending map[string]chan protocol.Header
}

func newLink(ws *websocket.Conn) *link {
	return &link{ws: ws, dead: make(chan struct{}), pending: map[string]chan protocol.Header{}}
}

func (l *link) write(ctx context.Context, b []byte) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return l.ws.Write(ctx, websocket.MessageText, b)
}

func (l *link) call(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	l.mu.Lock()
	l.nextID++
	id := strconv.Itoa(l.nextID)
	ch := make(chan protocol.Header, 1)
	l.pending[id] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.pending, id)
		l.mu.Unlock()
	}()
	frame, err := protocol.EncodeRequest(typ, id, params)
	if err != nil {
		return nil, err
	}
	if err := l.write(ctx, frame); err != nil {
		return nil, errNotConnected
	}
	select {
	case h := <-ch:
		if !h.OK {
			return nil, RemoteError(h.Error)
		}
		return h.Data, nil
	case <-l.dead:
		return nil, errNotConnected
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(callWait(ctx)):
		return nil, errors.New("gateway did not answer in time")
	}
}

func (l *link) readLoop(ctx context.Context, d *Daemon) {
	defer close(l.dead)
	for {
		_, raw, err := l.ws.Read(ctx)
		if err != nil {
			return
		}
		var h protocol.Header
		if json.Unmarshal(raw, &h) != nil {
			continue
		}
		switch h.Type {
		case "result":
			l.mu.Lock()
			ch := l.pending[h.ID]
			l.mu.Unlock()
			if ch != nil {
				ch <- h
			}
		case "node.upgrade":
			var p protocol.NodeUpgrade
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			go d.onUpgrade(ctx, l, h.ID, p.Version)
		case "msg.deliver":
			var p protocol.MsgDeliver
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			// "accepted for delivery"; the outcome follows as delivery.update.
			if res, err := protocol.EncodeResult(h.ID, map[string]bool{"accepted": true}, nil); err == nil {
				l.write(ctx, res)
			}
			d.enqueue(p.Message.To, func() { d.onDeliver(ctx, p.Message) })
		case "node.name":
			var p protocol.NodeName
			if json.Unmarshal(raw, &p) == nil && p.Hostname != "" {
				d.shownHost.Store(p.Hostname)
			}
		case "node.usage_top", "node.session_text", "node.session_enter":
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onOperatorFrame(ctx, l, h.Type, h.ID, raw)
			}()
		case "worker.launch", "worker.kill", "worker.resume", "worker.task", "worker.continue", "cto.worker_capabilities":
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onWorkerFrame(ctx, l, h, raw)
			}()
		case "mcp.sync", "mcp.probe", "mcp.invoke", "mcp.changed":
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onMCPFrame(ctx, l, h, raw)
			}()
		}
	}
}

// call sends a request over the current link.
func (d *Daemon) call(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	d.mu.Lock()
	l := d.lk
	d.mu.Unlock()
	if l == nil {
		return nil, errNotConnected
	}
	return l.call(ctx, typ, params)
}

func (d *Daemon) linkLoop(ctx context.Context) {
	delay := d.cfg.BackoffMin
	for ctx.Err() == nil {
		up, err := d.runLink(ctx)
		if ctx.Err() != nil {
			return
		}
		d.Logf("gateway link: %v", err)
		if up {
			delay = d.cfg.BackoffMin
		}
		wait := delay/2 + time.Duration(rand.Int63n(int64(delay/2)+1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay = min(delay*2, d.cfg.BackoffMax)
	}
}

// runLink connects, does the handshake and blocks until the link drops.
// up tells whether the handshake finished.
func (d *Daemon) runLink(ctx context.Context) (up bool, err error) {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	ws, _, err := websocket.Dial(dctx, d.cfg.GatewayURL+"/v1/node", &websocket.DialOptions{
		HTTPClient: d.client,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + d.cfg.Token}},
	})
	cancel()
	if err != nil {
		return false, err
	}
	ws.SetReadLimit(4 << 20)
	d.mu.Lock()
	d.connecting = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.connecting = false
		d.mu.Unlock()
	}()
	l := newLink(ws)
	rctx, rcancel := context.WithCancel(ctx)
	go l.readLoop(rctx, d)
	// readLoop adds to d.wg when a frame comes in. runLink is counted in d.wg
	// (through linkLoop), so it must not return while readLoop still runs:
	// an Add after the count has reached zero races with Stop's Wait.
	defer func() {
		ws.Close(websocket.StatusNormalClosure, "")
		rcancel()
		<-l.dead
	}()

	hctx, hcancel := context.WithTimeout(ctx, 20*time.Second)
	defer hcancel()
	inv, gen := d.inventory()
	hello, err := l.call(hctx, "hello", protocol.Hello{Version: d.helloVersion(), Hostname: d.cfg.Hostname, Harnesses: inv, Launch: d.launchFlag()})
	if err != nil {
		return false, err
	}
	var hr protocol.NodeName
	if json.Unmarshal(hello, &hr) == nil && hr.Hostname != "" {
		d.shownHost.Store(hr.Hostname)
	}
	snap := d.snapshot()
	data, err := l.call(hctx, "session.sync", protocol.SessionSync{Sessions: snap})
	if err != nil {
		return false, err
	}
	var r struct {
		Sessions []struct {
			NativeID  string `json:"native_id"`
			Harness   string `json:"harness"`
			SessionID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return false, err
	}
	d.mu.Lock()
	for _, s := range r.Sessions {
		if ls := d.sessions[key(s.Harness, s.NativeID)]; ls != nil {
			ls.sessionID = s.SessionID
		}
	}
	d.lk = l
	d.connecting = false
	// A session that is new or changed after the snapshot was taken was not
	// pushed (there was no link yet), so push it now.
	sent := map[string]protocol.SessionInfo{}
	for _, in := range snap {
		sent[key(in.Harness, in.NativeID)] = in
	}
	var missing []*localSession
	for k, ls := range d.sessions {
		if in, ok := sent[k]; ls.sessionID == "" || !ok || in != ls.info {
			missing = append(missing, ls)
		}
	}
	d.mu.Unlock()
	d.Logf("connected to gateway")
	if _, now := d.inventory(); now != gen {
		// Detection ended while the handshake ran, so hello did not carry it.
		d.publishHarnesses(rctx)
	}
	for _, ls := range missing {
		d.pushSession(ls)
	}
	d.flushOutbox(rctx)
	d.reportWorkerEnds(rctx)
	d.cleanupWorkers(rctx)
	go d.autoUpgrade(rctx) // not tracked: it may stop the daemon itself to restart

	select {
	case <-l.dead:
	case <-ctx.Done():
	}
	d.mu.Lock()
	if d.lk == l {
		d.lk = nil
	}
	d.mu.Unlock()
	return true, errors.New("link closed")
}
