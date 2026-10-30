package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Codex worker (decision 0008).
//
// Codex has no agent-net adapter and no way to load our tools into a session
// that we do not own, so the daemon drives it. A worker is `codex app-server`,
// Codex's long-lived mode for programs (`codex app-server --help`; the types
// come from `codex app-server generate-ts`, codex-cli 0.163.0):
//
//   - `--listen ws://127.0.0.1:<port>` with `--ws-auth capability-token` and
//     `--ws-token-file <path>`: a WebSocket on the loopback address, guarded by
//     a random token that is in a private file and not on the command line.
//     Unlike stdio, the server stays up when the daemon restarts, so the daemon
//     can connect again (adoption).
//   - The protocol is JSON-RPC. `initialize`, then `thread/start` with the
//     folder and the model, then `turn/start` with the text. The server sends
//     `item/completed` (the final answer is an agentMessage with phase
//     "final_answer"), `thread/tokenUsage/updated` and `turn/completed`.
//   - When a step needs approval, the server sends a request
//     (`item/commandExecution/requestApproval`, `item/fileChange/requestApproval`,
//     `item/permissions/requestApproval`, ...). We never approve: we decline,
//     and the turn ends with a hand-back of status blocked.
//
// So the daemon registers the thread as a session of harness codex, delivers
// each message from the owner as a turn, and turns the end of each turn into a
// hand-back (the report is the final answer). The model does not need a tool
// for it, and a hand-back cannot be forgotten. The cost is that the worker has
// no agent_reply for questions in the middle of the work, and takes messages
// from its owner and from the person only.
//
// The approval policy is "never" and the sandbox is the permission level of the
// project (worker_codex_sandbox.go, decision 0020, which amends 0002 W6). A
// request for approval that still arrives is declined.
type codexLauncher struct{}

func (codexLauncher) start(ctx context.Context, d *Daemon, w *worker) error {
	name, effort := splitCodexModel(w.model)
	if err := d.checkCodexEffort(ctx, name, effort); err != nil {
		return err
	}
	cc, err := d.startCodex(ctx, w)
	if err != nil {
		return err
	}
	params := map[string]any{"cwd": w.path}
	if name != "" {
		params["model"] = name
	}
	cc.sb = d.codexSandboxFor(ctx, w)
	cc.sb.threadParams(params)
	raw, err := cc.call(ctx, "thread/start", params)
	if err != nil {
		cc.close()
		return fmt.Errorf("codex refused the thread: %w", err)
	}
	var started struct {
		Thread struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &started) != nil || started.Thread.ID == "" {
		cc.close()
		return fmt.Errorf("codex answered without a thread ID: %s", truncate(string(raw), 200))
	}
	cc.thread = started.Thread.ID
	cc.effort = effort
	if effort != "" {
		// The worker shows the model it was started with, and its effort.
		cc.model = codexModelLabel(name, effort)
	} else {
		cc.model = firstNonEmpty(started.Model, started.Thread.Model, name)
	}
	d.wmu.Lock()
	w.codex = cc
	d.wmu.Unlock()
	if err := d.bindWorkerNative(w, cc.thread); err != nil {
		cc.close()
		return err
	}
	return d.registerCodexSession(ctx, w, true)
}

// codexEfforts are the reasoning efforts a Codex worker model may name after a
// colon. Codex lists the levels of each model in `codex debug models`
// (supported_reasoning_levels), and takes one on turn/start as "effort".
var codexEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// splitCodexModel splits "model:effort". A colon followed by anything else is
// part of the model name, as in a dated suffix.
func splitCodexModel(model string) (name, effort string) {
	i := strings.LastIndexByte(model, ':')
	if i <= 0 || i == len(model)-1 {
		return model, ""
	}
	effort = strings.ToLower(model[i+1:])
	for _, e := range codexEfforts {
		if e == effort {
			return model[:i], effort
		}
	}
	return model, ""
}

// codexModelLabel is what the worker shows: the model with its effort.
func codexModelLabel(model, effort string) string {
	if effort == "" {
		return model
	}
	return model + ":" + effort
}

// checkCodexEffort refuses an effort that the model does not list, and names
// the levels it does list. When the catalog cannot be read (an older Codex, or
// a model it does not know), the effort passes through and Codex decides.
func (d *Daemon) checkCodexEffort(ctx context.Context, model, effort string) error {
	if effort == "" {
		return nil
	}
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "debug", "models").Output()
	if err != nil {
		return nil
	}
	levels, known := codexSupportedEfforts(out, model)
	if !known {
		return nil
	}
	for _, l := range levels {
		if l == effort {
			return nil
		}
	}
	supported := strings.Join(levels, ", ")
	if supported == "" {
		supported = "none"
	}
	return fmt.Errorf("codex model %q does not support reasoning effort %q; it supports: %s", model, effort, supported)
}

// codexSupportedEfforts reads the output of `codex debug models`: one entry per
// model with its supported_reasoning_levels. known is false when the model is
// not in the catalog, or the output is not a catalog.
func codexSupportedEfforts(out []byte, model string) (levels []string, known bool) {
	var catalog struct {
		Models []struct {
			Slug   string `json:"slug"`
			Model  string `json:"model"`
			ID     string `json:"id"`
			Levels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(out, &catalog) != nil {
		return nil, false
	}
	for _, m := range catalog.Models {
		if m.Slug != model && m.Model != model && m.ID != model &&
			!strings.EqualFold(m.Slug, model) && !strings.EqualFold(m.Model, model) && !strings.EqualFold(m.ID, model) {
			continue
		}
		if m.Levels == nil {
			return nil, false
		}
		for _, l := range m.Levels {
			if l.Effort != "" {
				levels = append(levels, l.Effort)
			}
		}
		return levels, true
	}
	return nil, false
}

// resume starts a new private app server and reopens the worker's thread with
// thread/resume. Codex keeps the thread on disk, so the conversation and its
// folder continue.
func (codexLauncher) resume(ctx context.Context, d *Daemon, w *worker) error {
	if w.nativeID == "" {
		return errors.New("the Codex thread of the worker is not known")
	}
	cc, err := d.startCodex(ctx, w)
	if err != nil {
		return err
	}
	cc.sb = d.codexSandboxFor(ctx, w)
	rparams := map[string]any{"threadId": w.nativeID}
	cc.sb.threadParams(rparams)
	raw, err := cc.call(ctx, "thread/resume", rparams)
	if err != nil {
		cc.close()
		return fmt.Errorf("codex does not know the thread any more: %w", err)
	}
	var resumed struct {
		Thread struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"thread"`
	}
	name, effort := splitCodexModel(w.model)
	cc.effort = effort
	cc.model = codexModelLabel(name, effort)
	if json.Unmarshal(raw, &resumed) == nil && effort == "" {
		cc.model = firstNonEmpty(resumed.Thread.Model, name)
	}
	cc.thread = w.nativeID
	d.wmu.Lock()
	w.codex = cc
	d.wmu.Unlock()
	return d.registerCodexSession(ctx, w, true)
}

// startCodex starts the private app server and connects to it.
func (d *Daemon) startCodex(ctx context.Context, w *worker) (*codexConn, error) {
	bin, err := d.harnessBinary("codex")
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	dir := d.setWorkerDir(w)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	token := protocol.NewToken()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		return nil, err
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	args := []string{"app-server", "--listen", "ws://" + addr, "--ws-auth", "capability-token", "--ws-token-file", tokenPath}
	if codexConfigDefinesAgentNet() {
		// A worker has no use for the agent-net tools (its report is the end
		// of its turn), and Codex would ask approval to call them.
		args = append(args, "-c", "mcp_servers.agent-net.enabled=false")
	}
	if err := d.spawnWorker(w, bin, args, os.Environ()); err != nil {
		return nil, err
	}
	d.wmu.Lock()
	w.ref = addr
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
	return dialCodex(ctx, d, w, addr, token)
}

// adopt connects again to the app server of a worker that survived a restart
// of the daemon, and takes its thread back.
func (codexLauncher) adopt(ctx context.Context, d *Daemon, w *worker) error {
	tok, err := os.ReadFile(filepath.Join(d.setWorkerDir(w), "token"))
	if err != nil || w.ref == "" || w.nativeID == "" {
		return errors.New("the connection details of the Codex worker are gone")
	}
	cc, err := dialCodex(ctx, d, w, w.ref, strings.TrimSpace(string(tok)))
	if err != nil {
		return err
	}
	cc.thread = w.nativeID
	// The in-flight turn belongs to the last recorded request. Restore it
	// before subscribing: completion can arrive as soon as resume starts.
	if w.taskMsg == "" || w.owner == "" {
		cc.close()
		return errors.New("the request for the Codex turn is gone")
	}
	cc.running = true
	cc.cur = &codexJob{from: w.owner, msgID: w.taskMsg}
	name, effort := splitCodexModel(w.model)
	cc.effort, cc.model = effort, codexModelLabel(name, effort)
	cc.sb = d.codexSandboxFor(ctx, w)
	rparams := map[string]any{"threadId": w.nativeID}
	cc.sb.threadParams(rparams)
	raw, err := cc.call(ctx, "thread/resume", rparams)
	if err != nil {
		cc.close()
		return fmt.Errorf("codex does not know the thread any more: %w", err)
	}
	var resumed struct {
		Thread struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
			Turns []struct {
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
				Items []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &resumed); err != nil {
		cc.close()
		return fmt.Errorf("cannot read the resumed Codex thread: %w", err)
	}
	thread := resumed.Thread
	if thread.Status.Type != "active" && thread.Status.Type != "idle" {
		cc.close()
		return fmt.Errorf("cannot restore the Codex turn: thread status %q", thread.Status.Type)
	}
	if thread.Status.Type == "idle" {
		if len(thread.Turns) == 0 {
			cc.close()
			return errors.New("cannot restore the Codex turn: no turns in the resumed thread")
		}
		status := thread.Turns[len(thread.Turns)-1].Status
		if status != "completed" && status != "failed" && status != "interrupted" {
			cc.close()
			return fmt.Errorf("cannot restore the Codex turn: turn status %q", status)
		}
	}
	d.wmu.Lock()
	w.codex = cc
	d.wmu.Unlock()
	if err := d.registerCodexSession(ctx, w, false); err != nil {
		cc.close()
		return err
	}
	if thread.Status.Type == "active" {
		d.setCodexStatus(w, "busy")
		return nil
	}
	turn := thread.Turns[len(thread.Turns)-1]
	var final, errMsg string
	for _, item := range turn.Items {
		if item.Type == "agentMessage" && item.Text != "" {
			final = item.Text
		}
	}
	if turn.Error != nil {
		errMsg = turn.Error.Message
	}
	cc.mu.Lock()
	// A live completion can race the resume response. Do not overwrite it or
	// hand back a second time if the notification already finished the turn.
	if cc.cur != nil {
		cc.final = final
	}
	cc.mu.Unlock()
	cc.finishTurn(turn.Status, errMsg)
	return nil
}

// end closes the connection and takes the session out of the registry.
func (codexLauncher) end(d *Daemon, w *worker) {
	d.wmu.Lock()
	cc, native := w.codex, w.nativeID
	d.wmu.Unlock()
	if cc != nil {
		cc.close()
	}
	if native == "" {
		return
	}
	d.mu.Lock()
	ls := d.sessions[key("codex", native)]
	delete(d.sessions, key("codex", native))
	d.mu.Unlock()
	if ls != nil && ls.sessionID != "" {
		d.call(context.Background(), "session.remove", protocol.SessionRemove{SessionID: ls.sessionID})
	}
}

func (codexLauncher) stats(ctx context.Context, _ *Daemon, w *worker) workerStats {
	if w.codex == nil {
		return workerStats{}
	}
	return w.codex.stats()
}

// registerCodexSession makes the thread a session of the daemon, with the
// worker flag. At a start it waits until the gateway has given it an ID. After
// a restart it does not wait: the link may not be up yet, and the daemon sends
// all its sessions when it connects.
func (d *Daemon) registerCodexSession(ctx context.Context, w *worker, wait bool) error {
	d.wmu.Lock()
	native, model, path, pid := w.nativeID, w.model, w.path, w.pid
	cc := w.codex
	d.wmu.Unlock()
	if cc != nil && cc.model != "" {
		model = cc.model
	}
	info := protocol.SessionInfo{Harness: "codex", NativeID: native, PID: pid, Cwd: path,
		Status: "idle", Delivery: "codex-app-server", Repo: d.repo.Refresh(path),
		Mode: protocol.ModeBackground, Model: protocol.CleanModel(model), Worker: d.workerInfo(w)}
	ls := &localSession{info: info, owned: true}
	d.mu.Lock()
	d.sessions[key("codex", native)] = ls
	d.mu.Unlock()
	if !wait {
		go d.pushSession(ls)
		return nil
	}
	for {
		d.pushSession(ls)
		if sid := d.currentSessionID(ls); sid != "" {
			d.workerSessionReady(w, sid)
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("the gateway did not register the Codex worker session in time")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// setCodexStatus changes the status of the worker's session (idle or busy).
func (d *Daemon) setCodexStatus(w *worker, status string) {
	d.mu.Lock()
	ls := d.sessions[key("codex", w.nativeID)]
	if ls == nil || ls.info.Status == status {
		d.mu.Unlock()
		return
	}
	ls.info.Status = status
	d.mu.Unlock()
	d.pushSession(ls)
}

// ---- delivery ----

// deliverWorker delivers a message to a Codex worker, which is driven turn by
// turn in its private app server. The adapter (codex_peers.go) calls it for the
// sessions that are workers.
func (c *codexAdapter) deliverWorker(w *worker, m protocol.Message) Result {
	c.d.wmu.Lock()
	cc, owner := w.codex, w.owner
	c.d.wmu.Unlock()
	if cc == nil {
		return Result{Detail: "the Codex worker is not connected"}
	}
	person := m.FromHarness == protocol.OwnerHarness
	if m.From != owner && !person {
		return Result{Detail: "a Codex worker takes messages from the session that started it, and from the person at the machine, not from other agents"}
	}
	text := codexWorkerText(m)
	cc.submit(codexJob{from: m.From, msgID: m.ID, text: text})
	return Result{Delivered: true}
}

func codexWorkerText(m protocol.Message) string {
	text := m.Body
	const brief = "[agent-net worker brief]"
	if strings.HasPrefix(text, brief) {
		text = brief + protocol.NeutralizeMarkers(strings.TrimPrefix(text, brief))
	} else {
		text = protocol.NeutralizeMarkers(text)
	}
	if m.FromHarness == protocol.OwnerHarness && !strings.HasPrefix(text, "[agent-net worker brief]") {
		text = "Message from your user, in person:\n" + text
	}
	if m.ApprovalNote != "" {
		text += "\n[agent-net] " + m.ApprovalNote
	}
	return text
}

// ---- the connection ----

type codexJob struct{ from, msgID, text string }

type codexBlock struct{ action, reason string }

type codexConn struct {
	d *Daemon
	w *worker
	c *websocket.Conn

	wmu sync.Mutex // writes

	mu            sync.Mutex
	next          int
	pending       map[int]chan codexAnswer
	thread        string
	model         string
	effort        string // reasoning effort on every turn, from "model:effort"
	running       bool
	cur           *codexJob
	queue         []codexJob
	sb            codexSandbox // the permissions of the worker (decision 0020)
	final         string
	texts         []string
	blocked       []codexBlock
	sandboxDenied bool
	tokens        struct{ in, out, reasoning, cacheRead, cacheWrite float64 }
	// contextTokens and contextLimit come from the last turn's usage and the
	// model context window in thread/tokenUsage/updated.
	contextTokens, contextLimit int64
	closed                      bool
}

type codexAnswer struct {
	result json.RawMessage
	err    error
}

type codexMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// dialCodex connects to the app server (it may still be starting) and says
// initialize.
func dialCodex(ctx context.Context, d *Daemon, w *worker, addr, token string) (*codexConn, error) {
	var c *websocket.Conn
	for {
		var err error
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		c, _, err = websocket.Dial(dctx, "ws://"+addr, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
		cancel()
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("codex app-server did not accept a connection in time: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		if d.gone(w) {
			return nil, errors.New("codex app-server exited at start")
		}
	}
	c.SetReadLimit(32 << 20)
	cc := &codexConn{d: d, w: w, c: c, pending: map[int]chan codexAnswer{}}
	go cc.readLoop()
	if _, err := cc.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "agent-net", "title": nil, "version": d.helloVersion()}}); err != nil {
		cc.close()
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	if err := cc.write(map[string]any{"method": "initialized"}); err != nil {
		cc.close()
		return nil, err
	}
	return cc, nil
}

func (cc *codexConn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	cc.wmu.Lock()
	defer cc.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return cc.c.Write(ctx, websocket.MessageText, b)
}

func (cc *codexConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan codexAnswer, 1)
	cc.mu.Lock()
	cc.next++
	id := cc.next
	cc.pending[id] = ch
	cc.mu.Unlock()
	if err := cc.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		cc.mu.Lock()
		delete(cc.pending, id)
		cc.mu.Unlock()
		return nil, err
	}
	select {
	case a := <-ch:
		return a.result, a.err
	case <-ctx.Done():
		cc.mu.Lock()
		delete(cc.pending, id)
		cc.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (cc *codexConn) close() {
	cc.mu.Lock()
	cc.closed = true
	cc.mu.Unlock()
	cc.c.Close(websocket.StatusNormalClosure, "")
}

func (cc *codexConn) readLoop() {
	for {
		_, data, err := cc.c.Read(context.Background())
		if err != nil {
			cc.mu.Lock()
			closed := cc.closed
			for id, ch := range cc.pending {
				ch <- codexAnswer{err: errors.New("the connection to codex app-server ended")}
				delete(cc.pending, id)
			}
			cc.mu.Unlock()
			if !closed {
				cc.d.Logf("worker %s: the connection to codex app-server ended: %v", cc.w.id, err)
			}
			return
		}
		var m codexMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			cc.serverRequest(m)
		case m.Method != "":
			cc.notification(m)
		case len(m.ID) > 0:
			var id int
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			cc.mu.Lock()
			ch := cc.pending[id]
			delete(cc.pending, id)
			cc.mu.Unlock()
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

// serverRequest answers a request from the server. Nothing is ever approved.
func (cc *codexConn) serverRequest(m codexMsg) {
	var p struct {
		Command   string `json:"command"`
		Reason    string `json:"reason"`
		Cwd       string `json:"cwd"`
		GrantRoot string `json:"grantRoot"`
	}
	json.Unmarshal(m.Params, &p)
	var result any
	var block *codexBlock
	switch m.Method {
	case "item/commandExecution/requestApproval":
		result = map[string]any{"decision": "decline"}
		block = &codexBlock{action: "run the command: " + p.Command, reason: p.Reason}
	case "item/fileChange/requestApproval":
		result = map[string]any{"decision": "decline"}
		act := "change files"
		if p.GrantRoot != "" {
			act += " under " + p.GrantRoot
		}
		block = &codexBlock{action: act, reason: p.Reason}
	case "execCommandApproval", "applyPatchApproval":
		result = map[string]any{"decision": "denied"}
		block = &codexBlock{action: "run a command or change files (" + m.Method + ")", reason: p.Reason}
	case "mcpServer/elicitation/request":
		result = map[string]any{"action": "decline", "content": nil, "_meta": nil}
		block = &codexBlock{action: "answer a request from an MCP server", reason: p.Reason}
	case "item/permissions/requestApproval", "item/tool/requestUserInput":
		block = &codexBlock{action: "get extra permissions or an answer from a person (" + m.Method + ")", reason: p.Reason}
	}
	if block != nil {
		cc.mu.Lock()
		cc.blocked = append(cc.blocked, *block)
		cc.mu.Unlock()
	}
	var resp map[string]any
	if result != nil {
		resp = map[string]any{"id": m.ID, "result": result}
	} else {
		resp = map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "agent-net does not answer " + m.Method}}
	}
	if err := cc.write(resp); err != nil {
		cc.d.Logf("worker %s: answer to %s: %v", cc.w.id, m.Method, err)
	}
}

func (cc *codexConn) notification(m codexMsg) {
	var p struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type             string `json:"type"`
			Text             string `json:"text"`
			Phase            string `json:"phase"`
			Status           string `json:"status"`
			AggregatedOutput string `json:"aggregatedOutput"`
		} `json:"item"`
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message        string          `json:"message"`
				CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
			} `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	cc.mu.Lock()
	mine := p.ThreadID == "" || p.ThreadID == cc.thread
	cc.mu.Unlock()
	if !mine {
		return
	}
	switch m.Method {
	case "item/completed":
		if p.Item.Type == "commandExecution" && p.Item.Status == "failed" && sandboxDenial(p.Item.AggregatedOutput) {
			cc.mu.Lock()
			cc.sandboxDenied = true
			cc.mu.Unlock()
		}
		if p.Item.Type != "agentMessage" || p.Item.Text == "" {
			return
		}
		cc.mu.Lock()
		cc.texts = append(cc.texts, p.Item.Text)
		if p.Item.Phase == "final_answer" || p.Item.Phase == "" {
			cc.final = p.Item.Text
		}
		cc.mu.Unlock()
	case "thread/tokenUsage/updated":
		var u struct {
			TokenUsage struct {
				Last struct {
					Input      float64 `json:"inputTokens"`
					Cached     float64 `json:"cachedInputTokens"`
					CacheWrite float64 `json:"cacheWriteInputTokens"`
					Output     float64 `json:"outputTokens"`
					Reasoning  float64 `json:"reasoningOutputTokens"`
				} `json:"last"`
				Total struct {
					Input      float64 `json:"inputTokens"`
					Cached     float64 `json:"cachedInputTokens"`
					CacheWrite float64 `json:"cacheWriteInputTokens"`
					Output     float64 `json:"outputTokens"`
					Reasoning  float64 `json:"reasoningOutputTokens"`
				} `json:"total"`
				ModelContextWindow *float64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(m.Params, &u) != nil {
			return
		}
		last, t := u.TokenUsage.Last, u.TokenUsage.Total
		cc.mu.Lock()
		// Codex counts the cached tokens inside the input tokens. The tokens
		// the model saw on the last step are its input, cached or not, plus the
		// cache write.
		cc.contextTokens = int64(last.Input + last.CacheWrite)
		if u.TokenUsage.ModelContextWindow != nil {
			cc.contextLimit = int64(*u.TokenUsage.ModelContextWindow)
		}
		cc.tokens.in, cc.tokens.out, cc.tokens.reasoning = t.Input-t.Cached, t.Output, t.Reasoning
		cc.tokens.cacheRead, cc.tokens.cacheWrite = t.Cached, t.CacheWrite
		cc.mu.Unlock()
	case "turn/completed":
		errMsg := ""
		if p.Turn.Error != nil {
			errMsg = p.Turn.Error.Message
			if string(p.Turn.Error.CodexErrorInfo) == `"sandboxError"` {
				cc.mu.Lock()
				cc.sandboxDenied = true
				cc.mu.Unlock()
			}
		}
		cc.finishTurn(p.Turn.Status, errMsg)
	}
}

func (cc *codexConn) stats() workerStats {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	t := cc.tokens
	return workerStats{Tokens: tokenText(t.in, t.out, t.reasoning, t.cacheRead, t.cacheWrite), Model: cc.model,
		ContextTokens: cc.contextTokens, ContextLimit: cc.contextLimit,
		TotalTokens: int64(t.in + t.out + t.reasoning)}
}

// submit runs a message as a turn, or queues it behind the running one.
func (cc *codexConn) submit(j codexJob) {
	cc.mu.Lock()
	if cc.running {
		cc.queue = append(cc.queue, j)
		cc.mu.Unlock()
		return
	}
	cc.running = true
	cc.mu.Unlock()
	go cc.startTurn(j)
}

func (cc *codexConn) startTurn(j codexJob) {
	cc.mu.Lock()
	cc.cur = &j
	cc.final, cc.texts, cc.blocked = "", nil, nil
	cc.sandboxDenied = false
	thread, sb := cc.thread, cc.sb
	effort := cc.effort
	cc.mu.Unlock()
	cc.d.setCodexStatus(cc.w, "busy")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tparams := map[string]any{"threadId": thread, "approvalPolicy": "never",
		"input": []map[string]any{{"type": "text", "text": j.text}}}
	if effort != "" {
		tparams["effort"] = effort
	}
	if sb.policy != nil {
		tparams["sandboxPolicy"] = sb.policy
	}
	_, err := cc.call(ctx, "turn/start", tparams)
	if err != nil {
		cc.finishTurn("failed", "codex refused the turn: "+err.Error())
	}
}

// finishTurn turns the end of a turn into a hand-back, then starts the next
// queued message.
func (cc *codexConn) finishTurn(turnStatus, errMsg string) {
	cc.mu.Lock()
	j := cc.cur
	if !cc.running || j == nil {
		cc.mu.Unlock()
		return
	}
	cc.cur = nil
	final, texts, blocked, denied := cc.final, cc.texts, cc.blocked, cc.sandboxDenied || sandboxDenial(errMsg)
	cc.final, cc.texts, cc.blocked = "", nil, nil
	cc.mu.Unlock()
	if final == "" && len(texts) > 0 {
		final = texts[len(texts)-1]
	}
	status, report := protocol.StatusDone, final
	switch {
	case len(blocked) > 0:
		status, report = protocol.StatusBlocked, cc.blockedReport(blocked, final)
	case turnStatus == "failed":
		status = protocol.StatusFailed
		report = "The Codex turn failed: " + firstNonEmpty(errMsg, "no reason given") + ifText("\n\nLast message:\n", final)
	case turnStatus == "interrupted":
		status = protocol.StatusFailed
		report = "The Codex turn was interrupted." + ifText("\n\nLast message:\n", final)
	case strings.TrimSpace(report) == "":
		report = "(Codex ended the turn with no message.)"
	}
	var complete *bool
	if status == protocol.StatusDone {
		if v, ok := codexReportComplete(report); ok {
			complete = &v
		}
	}
	go func() {
		note := ""
		if denied && (status == protocol.StatusBlocked || status == protocol.StatusFailed) {
			note = "blocked by the sandbox (" + protocol.WorkerPermissionsOrDefault(cc.w.permissions) + ")"
		}
		_, err := cc.d.handbackToNote(context.Background(), cc.w, protocol.WorkerHandbackRequest{Report: report, Status: status, Complete: complete}, j.from, j.msgID, note)
		if err != nil {
			cc.d.Logf("worker %s: hand-back: %v", cc.w.id, err)
		}
		cc.mu.Lock()
		var next *codexJob
		if len(cc.queue) > 0 {
			n := cc.queue[0]
			cc.queue = cc.queue[1:]
			next = &n
		} else {
			cc.running = false
		}
		cc.mu.Unlock()
		if next != nil {
			cc.startTurn(*next)
			return
		}
		cc.d.setCodexStatus(cc.w, "idle")
	}()
}

// sandboxDenial requires explicit sandbox wording, not a generic permission error.
func sandboxDenial(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "sandbox") && (strings.Contains(s, "denied") || strings.Contains(s, "not permitted") || strings.Contains(s, "violation"))
}

// codexReportComplete reads the line the Codex brief asks for at the end of a
// report: "complete: yes" or "complete: no". The last such line wins. ok is
// false when the report has none (the work then counts as complete).
func codexReportComplete(report string) (complete, ok bool) {
	complete = true
	lines := strings.Split(strings.TrimSpace(report), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if line == "" {
			continue
		}
		switch line {
		case "complete: no":
			return false, true
		case "complete: yes":
			return true, true
		}
		return true, false
	}
	return true, false
}

// blockedReport is the W15 report: what was tried, why it stopped, and where
// the person can approve it.
func (cc *codexConn) blockedReport(blocked []codexBlock, final string) string {
	cc.d.wmu.Lock()
	path, host := cc.w.path, cc.d.hostName()
	cc.d.wmu.Unlock()
	var b strings.Builder
	b.WriteString("BLOCKED: the Codex worker needs an approval that it cannot get. agent-net never approves for the worker; it declined.\n")
	for i, x := range blocked {
		if i == 3 {
			fmt.Fprintf(&b, "(and %d more)\n", len(blocked)-3)
			break
		}
		fmt.Fprintf(&b, "action: %s\n", x.action)
		if x.reason != "" {
			fmt.Fprintf(&b, "reason: %s\n", x.reason)
		}
	}
	fmt.Fprintf(&b, "approve_in: Codex thread %s on %s (open it with `codex resume %s` in %s)\n", cc.thread, host, cc.thread, path)
	fmt.Fprintf(&b, "approval_text: Approve %s in the Codex session %s.\n", blocked[0].action, cc.thread)
	if final != "" {
		b.WriteString("\nLast message from the worker:\n" + final)
	}
	return b.String()
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func ifText(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}
