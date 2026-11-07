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
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
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
	// The stream reader owns its line buffer. mu guards input, the stream
	// checkpoint and figures shared with the daemon.
	streamID    string
	offset      int64
	stop        chan struct{}
	stopped     chan struct{}
	stopOnce    sync.Once
	buffer      []byte
	mu          sync.Mutex
	writeMu     sync.Mutex
	in          *os.File
	initialized chan string
	usage       agyUsage
	// turnUsage is this turn's sum of the per-step usage that step_update
	// reports. It is not saved: the result event repeats the run's total, and
	// that is what usage records.
	turnUsage  agyUsage
	completed  bool
	failure    string
	handback   *protocol.WorkerHandbackRequest
	pending    *protocol.WorkerHandbackRequest
	pendingKey string
	// Snapshots are kept in memory until the result event. After a daemon
	// restart the saved request is checked and prepared again instead.
	handbackFiles, pendingFiles *OutgoingFiles
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
	if d.workerStreamCtx != nil && d.workerStreamCtx.Err() != nil {
		return d.workerStreamCtx.Err()
	}
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
		old.closeStream()
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
	fifo := filepath.Join(dir, "agy-stdin")
	_ = os.Remove(fifo)
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		return err
	}
	rd, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer rd.Close()
	wr, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(dir, "agy-events.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		wr.Close()
		return err
	}
	defer out.Close()
	c := &agyConn{d: d, w: w, in: wr, streamID: protocol.NewID("s"), initialized: make(chan string, 1)}
	if b, err := os.ReadFile(filepath.Join(dir, "agy-usage.json")); err == nil {
		_ = json.Unmarshal(b, &c.usage)
	}
	d.wmu.Lock()
	native, model, permissions := w.nativeID, w.model, w.permissions
	w.agy = c
	d.wmu.Unlock()
	mode, settings := "accept-edits", agyWorkerSettings
	var gitDirs []string
	if permissions == protocol.WorkersReadOnly {
		mode, settings = "plan", agyReadOnlySettings
	} else {
		// A linked worktree's .git file points at the main repository's git
		// folder, which the sandbox hides. Without these narrow folders a
		// worker cannot run git status, and reading the .git file's target is
		// denied in headless mode.
		gitDirs = workerGitRoots(ctx, w.path)
	}
	env := d.workerGitEnv(w, os.Environ(), map[string]string{"AGENT_NET_WORKER": w.id, "AGENT_NET_SOCKET": d.cfg.SocketPath,
		"AGENT_NET_WORKER_SESSION_FILE": filepath.Join(dir, "session")})
	toolchains := d.workerToolchainDirs(ctx, w.path, env)
	d.wmu.Lock()
	w.toolchainDirs = toolchains
	d.wmu.Unlock()
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	// The gateway validated this id against the machine's agy inventory.
	selection := protocol.ParseModelSelection("agy", "", model, "")
	modelArgs, err := selection.Arguments([]string{selection.ModelID()})
	if err != nil {
		return err
	}
	args = append(args, modelArgs...)

	args = append(args, "--mode", mode, "--sandbox", "--settings", settings)
	for _, path := range toolchains {
		args = append(args, "--add-dir", path)
	}
	for _, path := range gitDirs {
		args = append(args, "--add-dir", path)
	}
	if native != "" {
		args = append(args, "--conversation", native)
	}
	args = append(args, "-p=")
	if err := d.spawnWorkerIO(w, bin, args, env, rd, out); err != nil {
		wr.Close()
		return err
	}
	c.mu.Lock()
	err = c.saveStreamLocked()
	c.mu.Unlock()
	if err != nil {
		wr.Close()
		return err
	}
	d.startAgyStream(c)
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

func (c *agyConn) event(line []byte) { c.eventAt(line, -1) }

func (c *agyConn) eventAt(line []byte, offset int64) {
	var e struct {
		Event        string `json:"event"`
		Conversation string `json:"conversation_id"`
		StepUpdate   struct {
			StepType string    `json:"step_type"`
			State    string    `json:"state"`
			Usage    *agyUsage `json:"usage"`
		} `json:"step_update"`
		Result struct {
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
		c.noteStepUsage(e.StepUpdate.StepType, e.StepUpdate.State, e.StepUpdate.Usage)
	case "result":
		r := e.Result
		c.mu.Lock()
		if offset >= 0 {
			c.offset = offset
		}
		c.usage.Input += r.Usage.Input
		c.usage.Output += r.Usage.Output
		c.usage.Thinking += r.Usage.Thinking
		c.usage.CacheRead += r.Usage.CacheRead
		c.turnUsage = agyUsage{}
		c.completed = true
		if r.Status != "SUCCESS" || r.Finished != "" {
			detail := r.Error
			if detail == "" {
				detail = r.Response
			}
			c.failure = cleanFailure("Antigravity result " + r.Status + " " + r.Finished + ": " + detail)
		} else if len(r.Denied) > 0 {
			c.failure = cleanFailure("Antigravity denied a tool action in headless mode: " + agyDeniedText(r.Denied))
		}
		usage, handback, failure := c.usage, c.handback, c.failure
		if handback != nil {
			c.pending, c.handback = handback, nil
			c.pendingFiles, c.handbackFiles = c.handbackFiles, nil
			c.pendingKey = fmt.Sprintf("agy-%s-%s-%d", w.id, c.streamID, c.offset)
		}
		var discarded *OutgoingFiles
		if failure != "" {
			discarded, c.pendingFiles = c.pendingFiles, nil
		}
		// Save usage and the acknowledged report with the consumed offset.
		// Keep the report until handbackTo has durably recorded the notice.
		_ = c.saveStreamLocked()
		c.mu.Unlock()
		if len(r.Denied) > 0 {
			d.Logf("worker.agy.denied worker=%s session=%s denied=%s", w.id, w.sessionID, agyDeniedText(r.Denied))
		}
		discarded.Remove()
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
			c.publishHandback(*handback)
		} else if !d.openCodeTurnIdle(w.sessionID) {
			report := r.Response
			if strings.TrimSpace(report) == "" {
				report = "(Antigravity ended the turn with no message.)"
			}
			d.noteCodexIdle(w, report)
		}
	}
}

// noteStepUsage records the context of a finished model step and the run's
// tokens so far. Antigravity reports per-step usage but no context window in
// its stream, so the limit comes from the model's family (agyContextLimit).
// The context is the input that step saw plus its cache reads. The result
// event repeats the run's sum, so this adds to turnUsage and never to usage:
// the result handler is the one that saves usage.
func (c *agyConn) noteStepUsage(stepType, state string, usage *agyUsage) {
	if usage == nil || stepType != "agent_response" || state != "DONE" {
		return
	}
	c.mu.Lock()
	c.turnUsage.Input += usage.Input
	c.turnUsage.Output += usage.Output
	c.turnUsage.Thinking += usage.Thinking
	c.turnUsage.CacheRead += usage.CacheRead
	base, turn := c.usage, c.turnUsage
	model := c.w.model
	c.mu.Unlock()
	c.d.noteWorkerUsage(c.w, workerStats{
		ContextTokens: int64(usage.Input + usage.CacheRead),
		ContextLimit:  agyContextLimit(model),
		TotalTokens:   int64(base.Input + base.Output + base.Thinking + turn.Input + turn.Output + turn.Thinking),
	})
}

func agyStats(u agyUsage, model string) workerStats {
	return workerStats{Tokens: tokenText(u.Input, u.Output, u.Thinking, u.CacheRead, 0), ContextLimit: agyContextLimit(model),
		TotalTokens: int64(u.Input + u.Output + u.Thinking), Model: model}
}

// agyContextLimit is the context window of an Antigravity model, by family.
// Antigravity shows the window in its own status line but does not report it
// in the stream-json events the daemon reads, so the number comes from the
// model vendor:
//   - Gemini 3 series and later, including Argon, have 1,048,576 input
//     tokens: https://ai.google.dev/gemini-api/docs/models
//   - Claude models use the same per-family table as the Claude adapter.
//   - GPT-OSS 120B has a 131,072-token context window:
//     https://github.com/openai/gpt-oss
//
// An unknown model has no limit (0), so the views show tokens only.
func agyContextLimit(model string) int64 {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "gemini-"):
		return 1_048_576
	case strings.HasPrefix(m, "claude-"):
		return claudeContextLimit(m)
	case strings.HasPrefix(m, "gpt-oss-"):
		return 131_072
	}
	return 0
}

// agyDeniedShown is how many denied actions a failure notice names. The rest
// are counted; the daemon log keeps the same short list.
const agyDeniedShown = 3

// agyDeniedText names the tool actions agy refused in headless mode. Each
// entry has a tool name and the tool's arguments as JSON; the text keeps a
// short, redacted path or command so the owner can tell what was blocked.
func agyDeniedText(denied []json.RawMessage) string {
	var parts []string
	for i, raw := range denied {
		if i == agyDeniedShown {
			parts = append(parts, fmt.Sprintf("and %d more", len(denied)-agyDeniedShown))
			break
		}
		parts = append(parts, agyDeniedOne(raw))
	}
	return strings.Join(parts, "; ")
}

func agyDeniedOne(raw json.RawMessage) string {
	var a struct {
		ToolName    string          `json:"tool_name"`
		ToolNameAlt string          `json:"toolName"`
		Tool        string          `json:"tool"`
		Args        json.RawMessage `json:"args"`
		Arguments   json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return redact.Args(s)
		}
		return redact.Args(string(raw))
	}
	tool := strings.TrimSpace(a.ToolName)
	if tool == "" {
		tool = strings.TrimSpace(a.ToolNameAlt)
	}
	if tool == "" {
		tool = strings.TrimSpace(a.Tool)
	}
	if tool == "" {
		tool = "a tool"
	}
	args := a.Args
	if len(args) == 0 || string(args) == "null" {
		args = a.Arguments
	}
	if args := agyDeniedArgs(args); args != "" {
		return tool + " " + args
	}
	return tool
}

// agyDeniedArgs picks the path or command out of a denied tool's arguments.
// Unknown shapes fall back to compact JSON. redact.Args masks secrets, removes
// control characters and cuts the text to 80 characters.
func agyDeniedArgs(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return redact.Args(s)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return redact.Args(string(raw))
	}
	for _, key := range []string{"Path", "path", "FilePath", "file_path", "CommandLine", "command", "Command"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			label := strings.ToLower(key)
			switch label {
			case "filepath", "file_path":
				label = "path"
			case "commandline":
				label = "command"
			}
			return redact.Args(label + "=" + v)
		}
	}
	b, _ := json.Marshal(m)
	return redact.Args(string(b))
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
	if agyFailedAfterExit(c, func() bool { return d.gone(w) }, exitErr) {
		return cleanFailure("Antigravity exited without a successful result: " + d.agyLogTail(w)), false
	}
	c.mu.Lock()
	failure := c.failure
	c.mu.Unlock()
	return failure, false
}

func agyFailedAfterExit(c *agyConn, gone func() bool, exitErr error) bool {
	exited := gone()
	c.mu.Lock()
	failure, completed := c.failure, c.completed
	c.mu.Unlock()
	if failure != "" {
		return false
	}
	if exited && (exitErr != nil || !completed) {
		return true
	}
	return false
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
		c.closeStream()
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
	// The inherited FIFO keeps stdin open. endWorker kills the process group.
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
	if err := c.saveStreamLocked(); err != nil {
		c.mu.Unlock()
		return err
	}
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
func (d *Daemon) deferAgyHandback(w *worker, req protocol.WorkerHandbackRequest, files *OutgoingFiles) (*protocol.WorkerHandbackResult, error) {
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
	c.handbackFiles = files
	if err := c.saveStreamLocked(); err != nil {
		c.handback = nil
		c.handbackFiles = nil
		return nil, err
	}
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
