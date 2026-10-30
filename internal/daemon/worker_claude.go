package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The Claude Code worker (decision 0002 W16, decision 0008).
//
// A worker is a Claude Code background session: `claude --bg`, not `claude -p`.
// The owner can `claude attach <id>` to it at any time, to watch it or to answer
// a permission prompt it waits on.
//
// Facts from Claude Code 2.1.312, checked on the real CLI:
//
//   - `claude --bg` starts the session in Claude's own background service and
//     returns at once. It prints "backgrounded · <id> · <name>" and
//     "claude stop <id>". The session is not a child of this daemon, and the
//     environment of the command is NOT passed on to it.
//   - The id is the first 8 characters of the session ID. The session writes
//     ~/.claude/sessions/<pid>.json with kind "bg", the same sessionId and
//     "jobId" set to the id, and an inbox socket (messagingSocketPath). The
//     Claude adapter finds it there like any session.
//   - `--settings <json>` reaches the session. "crossSessionInbound":"accept"
//     lets the agent-net messages in, and "env" is the way to set variables for
//     the session and its MCP servers (AGENT_NET_SOCKET). The user's own
//     settings.json is not touched.
//   - The agent-net MCP server (user scope) loads in the session as its child,
//     so agent_handback and agent_reply are there. The user's copy may be older
//     than this daemon, so the worker gets `--mcp-config` with this daemon's own
//     binary: a server of the same name there replaces the user's, for this
//     session only.
//   - The session process is the leader of its own process group.
//     `claude stop <id>` ends it and removes its session file.
//
// So the process of a worker is found from the session file, the binding is the
// session ID (the worker flag is put on the session the adapter discovers), and
// ending is `claude stop`, with the kill of the group as the fallback.
type claudeLauncher struct{}

var claudeBgID = regexp.MustCompile(`claude stop ([0-9A-Za-z-]+)`)

// claudeSessionFile is the part of a session file that matters here.
type claudeSessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Kind      string `json:"kind"`
}

func (claudeLauncher) start(ctx context.Context, d *Daemon, w *worker) error {
	return d.startClaude(ctx, w, "")
}

// resume starts a background session on the session the worker had before:
// `claude --bg --resume <session-id>`, with the same settings, model and
// MCP server. Claude keeps the transcript, so the conversation continues.
func (claudeLauncher) resume(ctx context.Context, d *Daemon, w *worker) error {
	if w.nativeID == "" {
		return errors.New("the Claude session of the worker is not known")
	}
	// The old MCP server is gone with the old process. Wait for the new one.
	d.mu.Lock()
	delete(d.mcpHello, w.nativeID)
	d.mu.Unlock()
	return d.startClaude(ctx, w, w.nativeID)
}

func (d *Daemon) startClaude(ctx context.Context, w *worker, resume string) error {
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return err
	}
	dir := d.setWorkerDir(w)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Per worker settings, as a JSON string for --settings. They add to the
	// settings of the user and the project and replace nothing in them.
	settings, _ := json.Marshal(map[string]any{
		"crossSessionInbound": "accept",
		"env": map[string]string{
			"AGENT_NET_SOCKET": d.cfg.SocketPath,
			"AGENT_NET_WORKER": w.id,
		},
	})
	// No flag that turns permission checks off (decision 0002, W6). The first
	// prompt is not an argument: the task arrives as a normal message.
	args := []string{"--bg", "--name", claudeWorkerName(w), "--settings", string(settings)}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	// The agent-net MCP server of the worker is this daemon's own binary, so the
	// tools it offers (agent_handback) are those of this version. --mcp-config
	// takes the place of the server of the same name in the user's config, for
	// this session only (checked on Claude Code 2.1.312).
	if d.cfg.Exe != "" {
		mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
			"agent-net": map[string]any{"command": d.cfg.Exe, "args": []string{"mcp"}}}})
		args = append(args, "--mcp-config", string(mcp))
	}
	if w.model != "" {
		args = append(args, "--model", w.model)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = w.path
	out, err := cmd.CombinedOutput()
	logf, lerr := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if lerr == nil {
		logf.Write(out)
		logf.Close()
	}
	if err != nil {
		return fmt.Errorf("claude --bg failed: %v", err) // its output is added from the log
	}
	m := claudeBgID.FindStringSubmatch(string(out))
	if m == nil {
		return errors.New("claude --bg did not say the background session id")
	}
	id := m[1]
	d.wmu.Lock()
	w.ref = id
	d.wmu.Unlock()

	// The session file gives the process and the session ID. On a resume the
	// job has a new id but the session keeps its own, so it is found by that.
	var sf claudeSessionFile
	find := func() (claudeSessionFile, bool) {
		if resume != "" {
			return findClaudeSession(d.cfg.ClaudeSessionsDir, resume)
		}
		return findClaudeBg(d.cfg.ClaudeSessionsDir, id)
	}
	for {
		if f, ok := find(); ok {
			sf = f
			break
		}
		select {
		case <-ctx.Done():
			claudeLauncher{}.stopID(d, id)
			return fmt.Errorf("the Claude background session %s wrote no session file in time", id)
		case <-time.After(50 * time.Millisecond):
		}
	}
	d.attachWorkerProcess(w, sf.PID)
	if err := d.bindWorkerNative(w, sf.SessionID); err != nil {
		return err
	}
	// The adapter discovers the session from the same file. Wait until the
	// daemon has it, with the worker flag, and the gateway has given it an ID.
	// The task is sent when the start returns, so the MCP server must be there
	// before: a turn that begins before Claude has connected its MCP servers
	// has no agent_handback (seen on Claude Code 2.1.312, where the first
	// message of a session started a turn at once, without the tools).
	for {
		if sid := d.workerSessionIDOf(w); sid != "" && d.claudeMCPReady(w) {
			d.workerSessionReady(w, sid)
			return nil
		}
		select {
		case <-ctx.Done():
			if d.workerSessionIDOf(w) != "" {
				return errors.New("the agent-net MCP server did not start in the Claude session in time: the worker could not hand back")
			}
			return errors.New("the daemon did not see the Claude session in time: is the Claude adapter running on this machine?")
		case <-time.After(25 * time.Millisecond):
		}
		if d.gone(w) {
			return errors.New("the Claude background session ended at start")
		}
	}
}

// claudeMCPReady tells whether the MCP server of the worker's session has said
// hello, and has had the settle time to finish its handshake with Claude.
func (d *Daemon) claudeMCPReady(w *worker) bool {
	d.wmu.Lock()
	native := w.nativeID
	d.wmu.Unlock()
	d.mu.Lock()
	at, ok := d.mcpHello[native]
	d.mu.Unlock()
	return ok && time.Since(at) >= d.cfg.Workers.filled().MCPSettle
}

// noteMCPHello records the first hello of the MCP server of a Claude session:
// the server starts with the session.
func (d *Daemon) noteMCPHello(native string) {
	d.mu.Lock()
	if _, ok := d.mcpHello[native]; !ok {
		d.mcpHello[native] = time.Now()
	}
	d.mu.Unlock()
}

func claudeWorkerName(w *worker) string {
	name := nameClean.ReplaceAllString(w.project, "-")
	if len(name) > 24 {
		name = name[:24]
	}
	return "worker-" + name + "-" + strings.TrimPrefix(w.id, "w_")
}

var nameClean = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// findClaudeBg finds the live background session with this id.
func findClaudeBg(dir, id string) (claudeSessionFile, bool) {
	return findClaudeSessionFile(dir, func(sf claudeSessionFile) bool {
		if sf.JobID != id && !(sf.JobID == "" && strings.HasPrefix(sf.SessionID, id)) {
			return false
		}
		return true
	})
}

// findClaudeSession finds the live session with this session ID. A resumed
// background job has a new job id but the same session id.
func findClaudeSession(dir, sessionID string) (claudeSessionFile, bool) {
	return findClaudeSessionFile(dir, func(sf claudeSessionFile) bool { return sf.SessionID == sessionID })
}

func findClaudeSessionFile(dir string, match func(claudeSessionFile) bool) (claudeSessionFile, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return claudeSessionFile{}, false
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var sf claudeSessionFile
		if json.Unmarshal(raw, &sf) != nil || sf.PID <= 1 || sf.SessionID == "" {
			continue
		}
		if !match(sf) {
			continue
		}
		if _, alive := procToken(sf.PID); !alive {
			continue
		}
		return sf, true
	}
	return claudeSessionFile{}, false
}

// workerSessionIDOf returns the gateway ID of the worker's session once the
// daemon has registered it with the worker flag, or "".
func (d *Daemon) workerSessionIDOf(w *worker) string {
	d.wmu.Lock()
	harness, native := w.harness, w.nativeID
	d.wmu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if ls := d.sessions[key(harness, native)]; ls != nil && ls.info.Worker != nil {
		return ls.sessionID
	}
	return ""
}

// stop is the polite end: `claude stop <id>`.
func (l claudeLauncher) stop(d *Daemon, w *worker) {
	d.wmu.Lock()
	id := w.ref
	d.wmu.Unlock()
	l.stopID(d, id)
}

func (claudeLauncher) stopID(d *Daemon, id string) {
	if id == "" {
		return
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, bin, "stop", id).CombinedOutput(); err != nil {
		d.Logf("worker: claude stop %s: %v: %s", id, err, truncate(strings.TrimSpace(string(out)), 200))
	}
}

// stats reads the tokens and the model of the session from its transcript.
// Claude Code does not write a cost, so there is none.
func (claudeLauncher) stats(ctx context.Context, d *Daemon, w *worker) workerStats {
	if w.nativeID == "" {
		return workerStats{}
	}
	return claudeTranscriptStats(filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects"), w.nativeID)
}

// claudeTranscriptStats sums the usage of the assistant messages in the
// transcript ~/.claude/projects/<folder>/<session>.jsonl. The context is the
// usage of the last assistant message, and the limit comes from
// claudeContextLimit.
func claudeTranscriptStats(projectsDir, session string) workerStats {
	matches, _ := filepath.Glob(filepath.Join(projectsDir, "*", session+".jsonl"))
	if len(matches) == 0 {
		return workerStats{}
	}
	f, err := os.Open(matches[0])
	if err != nil {
		return workerStats{}
	}
	defer f.Close()
	type usage struct{ in, out, cr, cw float64 }
	byMsg := map[string]usage{} // a message is written once per content block: keep the last
	model := ""
	var last usage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	n := 0
	for sc.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					In  float64 `json:"input_tokens"`
					Out float64 `json:"output_tokens"`
					CR  float64 `json:"cache_read_input_tokens"`
					CW  float64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" {
			continue
		}
		id := line.Message.ID
		if id == "" {
			n++
			id = "n" + strconv.Itoa(n)
		}
		u := line.Message.Usage
		byMsg[id] = usage{u.In, u.Out, u.CR, u.CW}
		last = byMsg[id]
		if line.Message.Model != "" && !strings.HasPrefix(line.Message.Model, "<") {
			model = line.Message.Model
		}
	}
	var t usage
	for _, u := range byMsg {
		t.in, t.out, t.cr, t.cw = t.in+u.in, t.out+u.out, t.cr+u.cr, t.cw+u.cw
	}
	st := workerStats{Tokens: tokenText(t.in, t.out, 0, t.cr, t.cw), Model: model,
		ContextTokens: int64(last.in + last.cr + last.cw), ContextLimit: claudeContextLimit(model),
		TotalTokens: int64(t.in + t.out)}
	return st
}

// claudeContextLimit is the context window of a Claude model, by family. The
// values are the ones in Claude Code 2.1.314's model list: the 5 series and
// Opus 4.7 and later have 1M, the rest have 200k. An unknown model has no
// limit (0), so the views show no percentage.
func claudeContextLimit(model string) int64 {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude-haiku-5"),
		strings.HasPrefix(m, "claude-sonnet-5"),
		strings.HasPrefix(m, "claude-opus-5"),
		strings.HasPrefix(m, "claude-opus-4-7"),
		strings.HasPrefix(m, "claude-opus-4-8"),
		strings.HasPrefix(m, "claude-fable-5"),
		strings.HasPrefix(m, "claude-mythos-5"):
		return 1_000_000
	case strings.HasPrefix(m, "claude-"):
		return 200_000
	}
	return 0
}
