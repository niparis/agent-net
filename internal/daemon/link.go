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
	"github.com/niparis/agent-net/internal/linkkeepalive"
	"github.com/niparis/agent-net/internal/protocol"
)

// link is one WebSocket connection to the gateway.
type link struct {
	ws     *websocket.Conn
	wmu    sync.Mutex
	dead   chan struct{}
	health *linkkeepalive.Monitor

	mu      sync.Mutex
	nextID  int
	pending map[string]chan protocol.Header
}

func newLink(ws *websocket.Conn, health *linkkeepalive.Monitor) *link {
	return &link{ws: ws, health: health, dead: make(chan struct{}), pending: map[string]chan protocol.Header{}}
}

func (l *link) write(ctx context.Context, b []byte) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return l.ws.Write(ctx, websocket.MessageText, b)
}

func (l *link) call(ctx context.Context, typ string, params any) (json.RawMessage, error) {
	via := viaFrom(ctx)
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
	frame, err := protocol.EncodeRequestVia(typ, id, via, params)
	if err != nil {
		return nil, err
	}
	if err := l.write(ctx, frame); err != nil {
		return nil, errNotConnected
	}
	select {
	case h := <-ch:
		if !h.OK {
			if h.Retry {
				return nil, retryableRemote{RemoteError(h.Error)}
			}
			return nil, RemoteError(h.Error)
		}
		return h.Data, nil
	case <-l.dead:
		return nil, errNotConnected
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(callWait(ctx)):
		return nil, errGatewayTimeout
	}
}

func (l *link) readLoop(ctx context.Context, d *Daemon) {
	defer close(l.dead)
	firstSettings := true
	for {
		_, raw, err := l.health.Read(ctx, l.ws)
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
		case protocol.FrameTopicChanged:
			var event protocol.TopicEvent
			if json.Unmarshal(raw, &event) == nil {
				d.topics.publish(l, event)
			}
		case protocol.FrameWorkersChanged:
			var ev protocol.WorkersChanged
			if json.Unmarshal(raw, &ev) == nil {
				d.workersChanged(l, ev)
			}
		case "node.token":
			var p struct {
				Token string `json:"token"`
			}
			if json.Unmarshal(raw, &p) != nil || p.Token == "" {
				continue
			}
			dir := d.cfg.NodeConfigDir
			if dir == "" {
				dir = ConfigDir()
			}
			err := saveRotatedToken(dir, d.cfg.NodeID, d.nodeToken(), p.Token)
			if err == nil {
				d.mu.Lock()
				d.cfg.Token = p.Token
				d.mu.Unlock()
			}
			if res, e := protocol.EncodeResult(h.ID, map[string]bool{"saved": err == nil}, err); e == nil {
				l.write(ctx, res)
			}
		case "node.upgrade":
			var p protocol.NodeUpgrade
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			go d.onUpgrade(ctx, l, h.ID, p.Version, p.Force)
		case "msg.deliver":
			var p protocol.MsgDeliver
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			// "accepted for delivery"; the outcome follows as delivery.update.
			if res, err := protocol.EncodeResult(h.ID, map[string]bool{"accepted": true}, nil); err == nil {
				l.write(ctx, res)
			}
			if len(p.Message.Files) > 0 {
				// Delivered only when every file is in (decision 0054).
				d.receiveFiles(p.Message)
				continue
			}
			d.enqueue(p.Message.To, func() { d.onDeliver(ctx, p.Message) })
		case protocol.FrameWaitClear:
			var p protocol.WaitClear
			if json.Unmarshal(raw, &p) == nil {
				d.wg.Add(1)
				go func() {
					defer d.wg.Done()
					d.onWaitClear(p.SessionID)
				}()
			}
		case "node.name":
			var p protocol.NodeName
			if json.Unmarshal(raw, &p) == nil && p.Hostname != "" {
				d.shownHost.Store(p.Hostname)
			}
		case "node.owner_ui.request", "node.machine_log", "node.machine_usage", "node.usage_top", "node.session_text", "node.session_enter", "node.message_body", "node.proposal_list", "node.proposal_answer":
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onOperatorFrame(ctx, l, h.Type, h.ID, raw)
			}()
		case "worker.launch", "worker.kill", "worker.validate_stop", "worker.resume", "worker.task", "worker.continue", "cto.worker_capabilities":
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onWorkerFrame(ctx, l, h, raw)
			}()
		case protocol.FrameSessionWake:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onSessionWakeFrame(ctx, l, h, raw)
			}()
		case protocol.FrameSessionOwnerStart, protocol.FrameSessionOwnerProposalStart, protocol.FrameSessionOwnerProposalClone, protocol.FrameSessionOwnerResume, protocol.FrameSessionOwnerStop, protocol.FrameSessionOwnerGet:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onSessionOwnerFrame(ctx, l, h, raw)
			}()
		case protocol.FrameSessionOwnerMCPServers:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onFolderMCPFrame(ctx, l, h, raw)
			}()
		case protocol.FrameSessionOwnerTrustFolder:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onFolderTrustFrame(ctx, l, h, raw)
			}()
		case protocol.FrameSessionOwnerStopProcess:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onProcessStopFrame(ctx, l, h, raw)
			}()
		case protocol.FrameProjectOwnerChange:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onProjectOwnerFrame(ctx, l, h, raw)
			}()
		case "node.projects_layout":
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.onProjectsLayout(ctx, l, h.ID)
			}()
		case "node.tools_changed":
			d.wakeTools()
		case "node.tool_plan_scan":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onToolPlanScan(ctx, l, h) }()
		case "node.tool_skill_scan":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onToolSkillScan(ctx, l, h, raw) }()
		case "node.tool_plan_probes":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onToolPlanProbes(ctx, l, h, raw) }()
		case "node.tool_plan_probe":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onToolPlanProbe(ctx, l, h, raw) }()
		case "node.mcp_import_spec":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onMCPImportSpec(ctx, l, h, raw) }()
		case "node.mcp_native_edit":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onMCPNativeEdit(ctx, l, h, raw) }()
		case "node.skill_text":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onSkillText(ctx, l, h, raw) }()
		case "node.skill_remove_copy":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onSkillRemoveCopy(ctx, l, h, raw) }()
		case "node.skill_update_copy":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onSkillUpdateCopy(ctx, l, h, raw) }()
		case "node.skill_folder":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onSkillFolder(ctx, l, h, raw) }()
		case "node.skill_bundle":
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.onSkillBundle(ctx, l, h, raw) }()
		case protocol.FrameCharterChanged:
			// A charter value or a team changed. The next turn of every session
			// reads the charter again (policy.go, graphread.go).
			d.charterEpoch.Add(1)
		case "settings.sync":
			if err := d.acceptSettingsSnapshot(raw, firstSettings); err == nil {
				firstSettings = false
			} else if d.log != nil {
				d.log.Printf("settings sync: %v", err)
			}
		case "mcp.sync", "mcp.probe", "mcp.invoke", "mcp.feature", "mcp.changed":
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
	if fake := d.cfg.testGatewayCall; fake != nil {
		if res, handled, err := fake(ctx, typ, params); handled {
			return res, err
		}
	}
	if actor := proposalActor(ctx); actor != "" && (typ == "session.trust" || typ == "held.answer" || typ == "node.rename") {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		fields["by"] = actor
		params = fields
	}

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
	health := linkkeepalive.New()
	ws, response, err := websocket.Dial(dctx, d.cfg.GatewayURL+"/v1/node", &websocket.DialOptions{
		HTTPClient:     health.Client(d.client),
		HTTPHeader:     http.Header{"Authorization": {"Bearer " + d.nodeToken()}, "X-Agent-Net-Node-ID": {d.cfg.NodeID}},
		OnPingReceived: func(context.Context, []byte) bool { health.Received(); return true },
		OnPongReceived: func(context.Context, []byte) { health.Received() },
	})
	cancel()
	if err != nil {
		if response != nil && response.Header.Get("Agent-Net-Removed") == "true" {
			return false, errors.New("this machine was removed from the network; run agent-net setup to join again")
		}
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
	l := newLink(ws, health)
	rctx, rcancel := context.WithCancel(ctx)
	health.Received()
	go health.Run(rctx, ws, d.cfg.KeepalivePingInterval, d.cfg.KeepaliveTimeout)
	go l.readLoop(rctx, d)
	// readLoop adds to d.wg when a frame comes in. runLink is counted in d.wg
	// (through linkLoop), so it must not return while readLoop still runs:
	// an Add after the count has reached zero races with Stop's Wait.
	defer func() {
		rcancel()
		ws.CloseNow()
		<-l.dead
	}()

	hctx, hcancel := context.WithTimeout(ctx, 20*time.Second)
	defer hcancel()
	inv, gen := d.inventory()
	hello, err := l.call(hctx, "hello", protocol.Hello{SessionAgents: true, GraphRefs: true, Version: d.helloVersion(), Hostname: d.cfg.Hostname, TimeZone: machineTimeZone(), Harnesses: inv, Launch: d.launchFlag(), TailscaleIP: d.fileConfig().TailscaleIP(), Health: d.machineHealth()})
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
	// A new link may have missed mcp.changed pushes: the next catalog read
	// asks the gateway.
	d.catalog.invalidate()
	// A session that is new or changed after the snapshot was taken was not
	// pushed (there was no link yet), so push it now.
	sent := map[string]protocol.SessionInfo{}
	for _, in := range snap {
		sent[key(in.Harness, in.NativeID)] = in
	}
	var missing []*localSession
	for k, ls := range d.sessions {
		info := d.infoLocked(ls)
		if in, ok := sent[k]; ls.sessionID == "" || !ok || in != info {
			missing = append(missing, ls)
		}
	}
	d.mu.Unlock()
	d.Logf("connected to gateway")
	d.scheduleCTOPresence()
	if d.toolsWake != nil {
		select {
		case d.toolsWake <- struct{}{}:
		default:
		}
	}
	if _, now := d.inventory(); now != gen {
		// Detection ended while the handshake ran, so hello did not carry it.
		d.publishHarnesses(rctx)
	}
	for _, ls := range missing {
		d.pushSession(ls)
	}
	// The outbox goes first, as before. keepOutbox then sends what is queued
	// while this link stays up. It ends when rctx is cancelled (runLink does
	// that on return), and d.wg counts it, so Stop waits for it.
	stalled := d.flushOutbox(rctx)
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.keepOutbox(rctx, stalled) }()
	d.flushWorkerAssessments(rctx)
	d.reportWorkerEnds(rctx)
	d.cleanupWorkers(rctx)
	go d.autoUpgrade(rctx) // not tracked: it may stop the daemon itself to restart

	select {
	case <-l.dead:
	case <-ctx.Done():
	}
	d.topics.disconnected(l)
	d.mu.Lock()
	if d.lk == l {
		d.lk = nil
	}
	d.mu.Unlock()
	return true, errors.New("link closed")
}
