package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
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
//     so agent_handback and agent_message_reply are there. The user's copy may be older
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
	Status    string `json:"status"`
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
	d.wmu.Lock()
	worktree := w.worktree
	d.wmu.Unlock()
	if worktree != "" && filepath.Clean(w.path) == filepath.Clean(worktree) {
		if err := claudetrust.Set(worktree); err != nil {
			return fmt.Errorf("trust Claude worker directory: %w", err)
		}
	}
	// Per worker settings, as a JSON string for --settings. They add to the
	// settings of the user and the project and replace nothing in them. The
	// commit trailer settings are also in the process environment; the settings
	// copy makes sure the worker's own shell commands see them too.
	commitEnv := d.workerCommitEnv(w)
	settingsEnv := map[string]string{
		"AGENT_NET_SOCKET":          d.cfg.SocketPath,
		"AGENT_NET_WORKER":          w.id,
		"CLAUDE_CODE_DISABLE_MOUSE": "1",
	}
	for k, v := range commitEnv {
		settingsEnv[k] = v
	}
	settings, _ := json.Marshal(map[string]any{
		"crossSessionInbound": "accept",
		"env":                 settingsEnv,
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
	modelArgs, err := protocol.ParseModelSelection("claude", "", w.model, "").Arguments(nil)
	if err != nil {
		return err
	}
	args = append(args, modelArgs...)

	d.claudeStartMu.Lock()
	locked := true
	defer func() {
		if locked {
			d.claudeStartMu.Unlock()
		}
	}()
	launchedJob := ""
	sf, err := d.launchClaudeBackground(ctx, bin, w.path, args, filepath.Join(dir, "worker.log"), resume, commitEnv, func(id string) {
		launchedJob = id
		d.wmu.Lock()
		w.ref = id
		d.wmu.Unlock()
	})
	if err != nil {
		claudeLauncher{}.stopID(d, launchedJob)
		return err
	}

	d.attachWorkerProcess(w, sf.PID)
	if resume != "" && sf.SessionID != resume {
		d.wmu.Lock()
		w.nativeID = sf.SessionID
		d.saveWorkerLocked(w)
		d.wmu.Unlock()
	}
	if err := d.bindWorkerNative(w, sf.SessionID); err != nil {
		return err
	}
	d.claudeStartMu.Unlock()
	locked = false
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
	return ok && time.Since(at) >= d.workerSettings("").MCPSettle
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
	harness, native, pid := w.harness, w.nativeID, w.pid
	d.wmu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if ls := d.sessions[key(harness, native)]; ls != nil && ls.info.Worker != nil && (harness != "claude" || ls.info.PID == pid) {
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

var claudeDirBad = regexp.MustCompile(`[^A-Za-z0-9]`)

func claudeTranscriptDir(cwd string) string {
	return claudeDirBad.ReplaceAllString(cwd, "-")
}

func claudeTranscriptPath(projectsDir, session, cwd string) string {
	if session == "" {
		return ""
	}
	name := session + ".jsonl"
	if cwd != "" {
		p := filepath.Join(projectsDir, claudeTranscriptDir(cwd), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	matches, _ := filepath.Glob(filepath.Join(projectsDir, "*", name))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// failure reads the worker's transcript tail and reports whether the latest
// assistant entry is a synthetic error or API error.
func (claudeLauncher) failure(_ context.Context, d *Daemon, w *worker) (string, bool) {
	d.wmu.Lock()
	native, cwd, off := w.nativeID, w.path, w.claudeTranscriptOff
	d.wmu.Unlock()
	if native == "" {
		return "", false
	}
	projectsDir := filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects")
	path := claudeTranscriptPath(projectsDir, native, cwd)
	if path == "" {
		return "", false
	}
	msg, provider, newOff := claudeTranscriptFailureAt(path, off)
	if newOff != off {
		d.wmu.Lock()
		w.claudeTranscriptOff = newOff
		d.wmu.Unlock()
	}
	return msg, provider
}

const claudeFailureMaxRead = 8 << 20

// claudeTranscriptFailureAt reads new complete lines in path from fromOff and
// reports if the newest assistant entry is a synthetic error or API error.
func claudeTranscriptFailureAt(path string, fromOff int64) (msg string, provider bool, newOff int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, fromOff
	}
	size := fi.Size()
	if fromOff < 0 || size < fromOff {
		fromOff = 0
	}
	if size <= fromOff {
		return "", false, fromOff
	}
	readStart := fromOff
	if size-readStart > claudeFailureMaxRead {
		readStart = size - claudeFailureMaxRead
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, fromOff
	}
	defer f.Close()

	buf := make([]byte, size-readStart)
	n, err := f.ReadAt(buf, readStart)
	if err != nil && err != io.EOF {
		return "", false, fromOff
	}
	buf = buf[:n]

	lastNl := bytes.LastIndexByte(buf, '\n')
	if lastNl < 0 {
		return "", false, fromOff
	}
	buf = buf[:lastNl+1]
	newOff = readStart + int64(lastNl) + 1

	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || !bytes.Contains(line, []byte(`"assistant"`)) {
			continue
		}
		var e struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			IsSidechain bool   `json:"isSidechain"`
			IsError     bool   `json:"isError"`
			Error       any    `json:"error"`
			Message     struct {
				Role       string          `json:"role"`
				Model      string          `json:"model"`
				StopReason string          `json:"stop_reason"`
				Content    json.RawMessage `json:"content"`
				Error      any             `json:"error"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.IsSidechain {
			continue
		}

		isSynthetic := strings.EqualFold(e.Message.Model, "<synthetic>") || (strings.HasPrefix(e.Message.Model, "<") && strings.Contains(e.Message.Model, "synthetic"))
		isError := isSynthetic || e.IsError || e.Subtype == "api_error" || e.Subtype == "error" || e.Message.StopReason == "error" || e.Error != nil || e.Message.Error != nil

		var text string
		if json.Unmarshal(e.Message.Content, &text) != nil {
			var blocks []struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Content json.RawMessage `json:"content"`
				IsError bool            `json:"is_error"`
			}
			if json.Unmarshal(e.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.IsError || b.Type == "error" || b.Type == "api_error" {
						isError = true
					}
					if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
						if text != "" {
							text += " "
						}
						text += b.Text
					}
				}
			}
		}
		if text == "" {
			text = claudeExtractError(e.Error)
			if text == "" {
				text = claudeExtractError(e.Message.Error)
			}
		}

		if !isError {
			// A normal assistant reply: not a failure.
			return "", false, newOff
		}

		cleaned := cleanFailure(text)
		return cleaned, claudeProviderFailure(text), newOff
	}

	return "", false, newOff
}

func claudeExtractError(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if msg, ok := m["message"].(string); ok {
			return msg
		}
		if errStr, ok := m["error"].(string); ok {
			return errStr
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// claudeProviderFailure classifies provider errors: model not found or no access,
// auth (401/403), quota, rate limit or capacity (429, usage limit, rate limit,
// at capacity, overloaded), or other API errors.
func claudeProviderFailure(text string) bool {
	s := strings.ToLower(text)
	for _, local := range []string{"sqlerror", "sqlite", "database", "tool.error", "tool error"} {
		if strings.Contains(s, local) {
			return false
		}
	}
	for _, m := range []string{"not exist", "no access", "not have access", "model not found", "model_not_found", "model unavailable", "model not available", "unknown model", "invalid model", "issue with the selected model"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	for _, m := range []string{"401", "403", "unauthorized", "forbidden", "not logged in", "authentication", "invalid api key", "invalid_api_key"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	for _, m := range []string{"429", "usage limit", "usage_limit", "rate limit", "rate_limit", "at capacity", "overloaded", "quota", "too many requests", "resource_exhausted", "credit balance"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	for _, m := range []string{"500", "502", "503", "504", "529", "api error", "api_error", "internal server error", "bad gateway", "service unavailable", "overloaded_error", "anthropic api"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// launchClaudeBackground is shared by workers and owner sessions. The caller
// holds claudeStartMu until the session file is known, before discovery runs.
// Worker settings, records and lifetime rules belong to the caller.
func (d *Daemon) launchClaudeBackground(ctx context.Context, bin, path string, args []string, logPath, resume string, extraEnv map[string]string, onJob func(string)) (claudeSessionFile, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = path
	env := os.Environ()
	if logPath == "" {
		filtered := env[:0]
		for _, item := range env {
			if !strings.HasPrefix(item, "AGENT_NET_WORKER=") && !strings.HasPrefix(item, "AGENT_NET_WORKER_") {
				filtered = append(filtered, item)
			}
		}
		env = filtered
	}
	set := map[string]string{"CLAUDE_CODE_DISABLE_MOUSE": "1"}
	for k, v := range extraEnv {
		set[k] = v
	}
	filtered := env[:0]
	for _, item := range env {
		k, _, _ := strings.Cut(item, "=")
		if (strings.HasPrefix(k, "GIT_CONFIG_") || strings.HasPrefix(k, "AGENT_NET_TRAILER_")) && len(extraEnv) > 0 {
			continue
		}
		filtered = append(filtered, item)
	}
	cmd.Env = workerEnv(filtered, set)
	out, err := cmd.CombinedOutput()
	if logPath != "" {
		logf, lerr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if lerr == nil {
			logf.Write(out)
			logf.Close()
		}
	}
	if err != nil {
		if claudeTrustPrompt(out) {
			return claudeSessionFile{}, fmt.Errorf("Claude Code does not trust %s", path)
		}
		return claudeSessionFile{}, fmt.Errorf("claude --bg failed: %v", err) // its output is added from the log
	}
	m := claudeBgID.FindStringSubmatch(string(out))
	if m == nil {
		return claudeSessionFile{}, errors.New("claude --bg did not say the background session id")
	}
	id := m[1]
	if onJob != nil {
		onJob(id)
	}

	// The launcher identifies the new job, including when resume changes the
	// native session ID. Match the job, not the old conversation ID.
	var sf claudeSessionFile
	find := func() (claudeSessionFile, bool) {
		return findClaudeBg(d.cfg.ClaudeSessionsDir, id)
	}
	for {
		if f, ok := find(); ok {
			sf = f
			break
		}
		select {
		case <-ctx.Done():
			if resume != "" {
				return claudeSessionFile{}, fmt.Errorf("the Claude background session %s wrote no session file in time; retry the message or resume the conversation with `claude --resume %s`", id, resume)
			}
			return claudeSessionFile{}, fmt.Errorf("the Claude background session %s wrote no session file in time; check Claude Code and retry the session start", id)
		case <-time.After(50 * time.Millisecond):
		}
	}
	return sf, nil
}
