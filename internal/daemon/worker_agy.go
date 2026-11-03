package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Antigravity workers use one stream-json process per conversation (0049 D2).
// -p= must be empty and LAST: otherwise agy consumes the next flag as a prompt.
// These one-run settings were checked on agy 1.4.12: accept-edits permits
// workspace edits, command(*) permits shell tools, and --sandbox makes home
// hidden or read-only while the workspace, /tmp and added toolchains remain
// writable. Nothing is saved to the user's settings. Auto mode is not available on every account.
type agyLauncher struct{}

const agyWorkerSettings = `{"permissions":{"allow":["command(*)","mcp(agent-net_agent-net/*)"]}}`
const agyReadOnlySettings = `{"permissions":{"allow":["mcp(agent-net_agent-net/*)"]}}`

type agyUsage struct {
	Input     float64 `json:"input_tokens"`
	Output    float64 `json:"output_tokens"`
	Thinking  float64 `json:"thinking_tokens"`
	CacheRead float64 `json:"cache_read_tokens"`
}

type agyConn struct {
	d *Daemon
	w *worker
	// Write is called by exec's stdout copier. Its line buffer is private to
	// that goroutine; mu guards input and figures shared with the daemon.
	buffer      []byte
	mu          sync.Mutex
	writeMu     sync.Mutex
	in          *os.File
	initialized chan string
	usage       agyUsage
	completed   bool
	failure     string
	handback    *protocol.WorkerHandbackRequest
}

func (agyLauncher) start(ctx context.Context, d *Daemon, w *worker) error {
	return d.startAgy(ctx, w)
}

func (agyLauncher) resume(ctx context.Context, d *Daemon, w *worker) error {
	d.wmu.Lock()
	native := w.nativeID
	d.wmu.Unlock()
	if native == "" {
		return errors.New("the Antigravity conversation of the worker is not known")
	}
	return d.startAgy(ctx, w)
}

func (d *Daemon) startAgy(ctx context.Context, w *worker) error {
	w.agyProcessMu.Lock()
	defer w.agyProcessMu.Unlock()
	d.wmu.Lock()
	if w.stopping {
		d.wmu.Unlock()
		return errors.New("the Antigravity worker is ending")
	}
	w.agyStarting = true
	old := w.agy
	exited := w.exited
	d.wmu.Unlock()
	defer func() { d.wmu.Lock(); w.agyStarting = false; d.wmu.Unlock() }()
	if old != nil {
		old.mu.Lock()
		if old.in != nil {
			old.in.Close()
			old.in = nil
		}
		old.mu.Unlock()
	}
	// Reap any stragglers and drain the old stream before reading its usage.
	d.killGroup(w)
	if exited != nil {
		select {
		case <-exited:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	bin, err := d.harnessBinary("agy")
	if err != nil {
		return err
	}
	dir := d.setWorkerDir(w)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		return err
	}
	defer rd.Close()
	c := &agyConn{d: d, w: w, in: wr, initialized: make(chan string, 1)}
	if b, err := os.ReadFile(filepath.Join(dir, "agy-usage.json")); err == nil {
		_ = json.Unmarshal(b, &c.usage)
	}
	d.wmu.Lock()
	native, model, permissions := w.nativeID, w.model, w.permissions
	w.agy = c
	d.wmu.Unlock()
	mode, settings := "accept-edits", agyWorkerSettings
	if permissions == protocol.WorkersReadOnly {
		mode, settings = "plan", agyReadOnlySettings
	}
	env := d.workerGitEnv(w, os.Environ(), map[string]string{"AGENT_NET_WORKER": w.id, "AGENT_NET_SOCKET": d.cfg.SocketPath,
		"AGENT_NET_WORKER_SESSION_FILE": filepath.Join(dir, "session")})
	toolchains := d.workerToolchainDirs(ctx, w.path, env)
	d.wmu.Lock()
	w.toolchainDirs = toolchains
	d.wmu.Unlock()
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	if model != "" {
		name, effort := protocol.SplitModelLevel(model)
		args = append(args, "--model", name)
		if effort != "" {
			args = append(args, "--effort", effort)
		}
	}
	args = append(args, "--mode", mode, "--sandbox", "--settings", settings)
	for _, path := range toolchains {
		args = append(args, "--add-dir", path)
	}
	if native != "" {
		args = append(args, "--conversation", native)
	}
	args = append(args, "-p=")
	if err := d.spawnWorkerIO(w, bin, args, env, rd, c); err != nil {
		wr.Close()
		return err
	}
	ok := false
	defer func() {
		if !ok {
			wr.Close()
		}
	}()
	d.wmu.Lock()
	exited = w.exited
	d.wmu.Unlock()
	var id string
	select {
	case id = <-c.initialized:
	case <-ctx.Done():
		return fmt.Errorf("Antigravity did not initialize: %w", ctx.Err())
	case <-exited:
		return fmt.Errorf("Antigravity exited at start: %s", d.workerLogTail(w))
	}
	if err := d.bindWorkerNative(w, id); err != nil {
		return err
	}
	if err := writeWorkerSessionFile(w, id); err != nil {
		return err
	}
	if err := d.registerAgySession(ctx, w); err != nil {
		return err
	}
	ok = true
	return nil
}

func (d *Daemon) registerAgySession(ctx context.Context, w *worker) error {
	d.wmu.Lock()
	native, model, path, pid, sid := w.nativeID, w.model, w.path, w.pid, w.sessionID
	d.wmu.Unlock()
	ls := &localSession{owned: true, sessionID: sid, info: protocol.SessionInfo{Harness: "agy", NativeID: native, PID: pid, Cwd: path,
		Status: "idle", Delivery: "stream-json", Mode: protocol.ModeBackground, Model: protocol.CleanModel(model), Repo: d.repo.Refresh(path), Worker: d.workerInfo(w)}}
	d.mu.Lock()
	ls.lastActive = d.activity[key("agy", native)]
	d.sessions[key("agy", native)] = ls
	d.mu.Unlock()
	for {
		d.pushSession(ls)
		if sid := d.currentSessionID(ls); sid != "" {
			d.workerSessionReady(w, sid)
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("the gateway did not register the Antigravity worker session in time")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (c *agyConn) Write(p []byte) (int, error) {
	c.buffer = append(c.buffer, p...)
	for {
		i := bytes.IndexByte(c.buffer, '\n')
		if i < 0 {
			break
		}
		c.event(c.buffer[:i])
		c.buffer = c.buffer[i+1:]
	}
	if len(c.buffer) > 16<<20 {
		c.mu.Lock()
		c.failure = "Antigravity emitted a stream event larger than 16 MiB"
		c.mu.Unlock()
		c.buffer = nil
	}
	return len(p), nil
}

func (c *agyConn) event(line []byte) {
	var e struct {
		Event        string `json:"event"`
		Conversation string `json:"conversation_id"`
		Result       struct {
			Conversation string            `json:"conversation_id"`
			Status       string            `json:"status"`
			Response     string            `json:"response"`
			Error        string            `json:"error"`
			Finished     string            `json:"finished_reason"`
			Usage        agyUsage          `json:"usage"`
			Denied       []json.RawMessage `json:"denied_actions"`
		} `json:"result"`
	}
	if json.Unmarshal(line, &e) != nil {
		return
	}
	d, w := c.d, c.w
	switch e.Event {
	case "init":
		if e.Conversation != "" {
			select {
			case c.initialized <- e.Conversation:
			default:
			}
		}
	case "step_update":
		d.wmu.Lock()
		w.turnAwaitingStart = false
		d.wmu.Unlock()
		d.keepWorkerActive(w, time.Now())
		d.setAgyStatus(w, "busy")
	case "result":
		r := e.Result
		c.mu.Lock()
		c.usage.Input += r.Usage.Input
		c.usage.Output += r.Usage.Output
		c.usage.Thinking += r.Usage.Thinking
		c.usage.CacheRead += r.Usage.CacheRead
		c.completed = true
		if r.Status != "SUCCESS" || r.Finished != "" {
			detail := r.Error
			if detail == "" {
				detail = r.Response
			}
			c.failure = cleanFailure("Antigravity result " + r.Status + " " + r.Finished + ": " + detail)
		} else if len(r.Denied) > 0 {
			c.failure = "Antigravity denied a tool action in headless mode"
		}
		usage, handback, failure := c.usage, c.handback, c.failure
		c.handback = nil
		c.mu.Unlock()
		b, _ := json.Marshal(usage)
		tmp := filepath.Join(w.dir, "agy-usage.json.tmp")
		if err := os.WriteFile(tmp, b, 0600); err == nil {
			_ = os.Rename(tmp, filepath.Join(w.dir, "agy-usage.json"))
		}
		d.noteWorkerUsage(w, agyStats(usage, w.model))
		d.setAgyStatus(w, "idle")
		if failure != "" {
			return
		}
		if handback != nil {
			d.goBackground(func() {
				if out, err := d.handbackTo(context.Background(), w, *handback, "", ""); err != nil {
					d.Logf("worker %s: Antigravity handback: %v", w.id, err)
				} else if out.AutoContinued {
					// The automatic continue was queued behind this turn.
					d.openCodeTurnIdle(w.sessionID)
				}
			})
		} else if !d.openCodeTurnIdle(w.sessionID) {
			report := r.Response
			if strings.TrimSpace(report) == "" {
				report = "(Antigravity ended the turn with no message.)"
			}
			d.noteCodexIdle(w, report)
		}
	}
}

func agyStats(u agyUsage, model string) workerStats {
	return workerStats{Tokens: tokenText(u.Input, u.Output, u.Thinking, u.CacheRead, 0), TotalTokens: int64(u.Input + u.Output + u.Thinking), Model: model}
}

func (agyLauncher) stats(_ context.Context, d *Daemon, w *worker) workerStats {
	d.wmu.Lock()
	c, model := w.agy, w.model
	d.wmu.Unlock()
	if c == nil {
		return workerStats{}
	}
	c.mu.Lock()
	u := c.usage
	c.mu.Unlock()
	return agyStats(u, model)
}

func (agyLauncher) failure(_ context.Context, d *Daemon, w *worker) (string, bool) {
	d.wmu.Lock()
	c, exitErr, starting := w.agy, w.exitErr, w.agyStarting
	d.wmu.Unlock()
	if c == nil || starting {
		return "", false
	}
	c.mu.Lock()
	failure, completed := c.failure, c.completed
	c.mu.Unlock()
	if failure != "" {
		return failure, false
	}
	if d.gone(w) && (exitErr != nil || !completed) {
		return cleanFailure("Antigravity exited without a successful result: " + d.agyLogTail(w)), false
	}
	return "", false
}

func agyTurnFinished(d *Daemon, w *worker) bool {
	d.wmu.Lock()
	c := w.agy
	starting := w.agyStarting
	d.wmu.Unlock()
	if starting {
		return true
	}
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.completed && c.failure == ""
}

func (agyLauncher) stop(d *Daemon, w *worker) {
	w.agyProcessMu.Lock()
	defer w.agyProcessMu.Unlock()
	d.wmu.Lock()
	c, exited := w.agy, w.exited
	d.wmu.Unlock()
	if c != nil {
		c.mu.Lock()
		if c.in != nil {
			c.in.Close()
			c.in = nil
		}
		c.mu.Unlock()
	}
	if exited != nil {
		select {
		case <-exited:
		case <-time.After(500 * time.Millisecond):
		}
	}
	// endWorker follows this grace period with killGroup, including descendants.
}

func (d *Daemon) setAgyStatus(w *worker, status string) {
	d.wmu.Lock()
	native := w.nativeID
	d.wmu.Unlock()
	d.mu.Lock()
	ls := d.sessions[key("agy", native)]
	if ls != nil {
		ls.info.Status = status
		d.recordActivityLocked(ls, time.Now())
	}
	d.mu.Unlock()
	if ls != nil {
		d.persistActivity(ls)
		go d.pushSession(ls)
	}
}

func (d *Daemon) deliverAgy(ctx context.Context, w *worker, m protocol.Message) Result {
	d.wmu.Lock()
	ending := w.stopping || w.doneStatus != "" || d.workers[w.id] != w
	d.wmu.Unlock()
	if ending {
		return Result{Detail: "the Antigravity worker is ending"}
	}
	if d.gone(w) {
		d.resumeMu.Lock()
		var err error
		if d.gone(w) {
			err = (agyLauncher{}).resume(ctx, d, w)
		}
		d.resumeMu.Unlock()
		if err != nil {
			d.wmu.Lock()
			w.failure = cleanFailure(err.Error())
			d.wmu.Unlock()
			d.goBackground(func() { d.endWorker(w, protocol.StopRunFailed) })
			return Result{Detail: err.Error()}
		}
	}
	d.wmu.Lock()
	c, stopping := w.agy, w.stopping
	d.wmu.Unlock()
	if c == nil || stopping {
		return Result{Detail: "the Antigravity worker is ending"}
	}
	d.resetIdleNotice(w)
	// Set busy before writing: a fast result must be the last status change.
	d.setAgyStatus(w, "busy")
	text := d.workerToolchainPermissions(w, codexWorkerText(m))
	line, _ := json.Marshal(map[string]any{"event": "user", "message": map[string]string{"content": text}})
	err := c.send(append(line, '\n'))
	if err != nil {
		return Result{Detail: "Antigravity input: " + err.Error()}
	}
	return Result{Delivered: true}
}

// Closing stdin must interrupt a blocked write, even if the worker has stopped
// reading. Do not hold the state mutex across pipe I/O.
func (c *agyConn) send(line []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	in := c.in
	if in == nil {
		c.mu.Unlock()
		return errors.New("the Antigravity input is closed")
	}
	c.completed = false
	c.mu.Unlock()
	_, err := in.Write(line)
	if err != nil {
		c.mu.Lock()
		c.failure = cleanFailure("Antigravity input: " + err.Error())
		c.mu.Unlock()
	}
	return err
}

// agy sends usage only after the MCP handback call has returned. Acknowledge
// the tool now and publish the report on result, before ending the process.
func (d *Daemon) deferAgyHandback(w *worker, req protocol.WorkerHandbackRequest) (*protocol.WorkerHandbackResult, error) {
	if req.Status == "" {
		req.Status = protocol.StatusDone
	}
	if !protocol.ValidHandbackStatus(req.Status) {
		return nil, errors.New("status must be done, blocked or failed")
	}
	if strings.TrimSpace(req.Report) == "" {
		return nil, errors.New("the report is empty: say what you did, or what blocks you")
	}
	d.wmu.Lock()
	c, stopping := w.agy, w.stopping
	d.wmu.Unlock()
	if c == nil || stopping {
		return nil, errors.New("the Antigravity worker is ending")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handback != nil {
		return nil, errors.New("the turn already has a handback")
	}
	c.handback = &req
	return &protocol.WorkerHandbackResult{Status: req.Status, Complete: req.IsComplete()}, nil
}

// The plugin's MCP shim passes _meta["antigravity.google/root_conversation_id"]
// as native_id. Both it and the kernel's process group must match the worker.
// A tool-listing connection has no native_id and cannot use session methods.
func (lc *localConn) helloAgyMCP(p helloParams, pid int) (any, error) {
	d := lc.d
	w := d.workerForPeer(pid)
	if w == nil || w.harness != "agy" {
		return nil, errors.New("peer check failed: the MCP process is not in an Antigravity worker's process group")
	}
	d.wmu.Lock()
	native, stopping := w.nativeID, w.stopping
	d.wmu.Unlock()
	if stopping {
		return nil, errors.New("the Antigravity worker is ending")
	}
	if p.NativeID == "" {
		lc.mu.Lock()
		lc.client = "mcp"
		lc.peerPID = pid
		lc.mu.Unlock()
		return map[string]any{"node_id": d.cfg.NodeID, "version": d.helloVersion()}, nil
	}
	if native == "" || p.NativeID != native {
		return nil, errors.New("peer check failed: root_conversation_id does not match the Antigravity worker")
	}
	d.mu.Lock()
	ls := d.sessions[key("agy", native)]
	d.mu.Unlock()
	if ls == nil {
		return nil, errors.New("the daemon has not registered this Antigravity worker")
	}
	lc.bind("mcp", ls)
	lc.mu.Lock()
	lc.peerPID = pid
	lc.mu.Unlock()
	return d.mcpHelloResult(ls, "agy", p.Version), nil
}

// agyWorkerConversation reports whether an agy worker owns this conversation ID.
func (d *Daemon) agyWorkerConversation(native string) bool {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	for _, w := range d.workers {
		if w.harness == "agy" && w.nativeID == native {
			return true
		}
	}
	return false
}

// agyLogTail is the end of the worker log without agy's stream-json events.
// The log holds both streams; the events would push the error text, which
// agy writes to stderr last, out of the failure note.
func (d *Daemon) agyLogTail(w *worker) string {
	d.wmu.Lock()
	logPath := w.logPath
	d.wmu.Unlock()
	raw, err := os.ReadFile(logPath)
	if logPath == "" || err != nil {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		var event struct {
			Event string `json:"event"`
		}
		if line == "" || json.Unmarshal([]byte(line), &event) == nil && event.Event != "" {
			continue
		}
		kept = append(kept, line)
	}
	s := strings.Join(kept, " ")
	if len(s) > 400 {
		s = "..." + s[len(s)-400:]
	}
	return strings.Join(strings.Fields(s), " ")
}
