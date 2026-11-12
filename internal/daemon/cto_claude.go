package daemon

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/claudetext"
	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// claudeCTOState runtime fields are protected by cto.mu. The process root and
// starting flag are atomic. capMu serializes the capability check and its
// cache, which a start from the owner and one from a proposal note both use.
type claudeCTOState struct {
	root                atomic.Pointer[claudeCTORoot]
	starting            atomic.Bool
	capMu               sync.Mutex
	token               string
	ref, session, model string
	capabilities        map[string]claudeCTOCapability
	restarted           bool
}

type claudeCTOCapability struct {
	mtime time.Time
	err   error
}

type claudeCTORoot struct {
	pid   int
	token string
}

type claudeCTOHarness struct{}

func (claudeCTOHarness) name() string { return "claude" }

// IsClaudeCTOJob says whether job is the Claude background job bound as the
// CTO's conversation. The Claude adapter compacts that job at the cto agent
// level, and the owner's open attach client does not stop it.
func (d *Daemon) IsClaudeCTOJob(job string) bool {
	if job == "" {
		return false
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	return job == d.claudeCTO.ref
}

// releaseClaudeCTOLocked forgets the CTO's Claude job without ending it.
func (d *Daemon) releaseClaudeCTOLocked() {
	c := &d.claudeCTO
	c.root.Store(nil)
	c.ref, c.session, c.model, c.token = "", "", "", ""
}

func (d *Daemon) stopClaudeCTOLocked(reasons ...string) {
	reason := "requested"
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	c := &d.claudeCTO
	root := c.root.Load()
	wasRunning := c.ref != ""
	var crashState, crashDetail string
	if wasRunning && reason == "crash" {
		crashState, crashDetail = claudeCTOJobState(d.cfg.ClaudeSessionsDir, c.ref)
		crashState = claudeCTOErrorOutput([]byte(crashState))
	}
	if c.ref != "" {
		(claudeLauncher{}).stopID(d, c.ref)
	}
	if root != nil {
		token, alive := procToken(root.pid)
		if alive && token == root.token {
			pgid, err := syscall.Getpgid(root.pid)
			if err == nil && pgid == root.pid {
				_ = syscall.Kill(-root.pid, syscall.SIGKILL)
			} else {
				_ = syscall.Kill(root.pid, syscall.SIGKILL)
			}
		}
	}
	c.root.Store(nil)
	c.ref, c.session, c.model, c.token = "", "", "", ""
	if wasRunning {
		if reason == "crash" {
			d.Logf("cto.crash harness=claude reason=job_ended state=%q detail=%q", crashState, crashDetail)
		}
		d.Logf("cto.stop harness=claude reason=%s", reason)
		d.scheduleCTOPresence()
	}
}

func sameCTODir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if real, err := filepath.EvalSymlinks(a); err == nil {
		a = real
	}
	if real, err := filepath.EvalSymlinks(b); err == nil {
		b = real
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// findClaudeCTOBg binds only the background job launched in the private CTO
// working directory. The PID file name must match the session's PID. An empty
// ref allows adoption of any matching live job after a daemon restart.
func findClaudeCTOBg(sessions, dir, ref string) (claudeSessionFile, bool) {
	entries, err := os.ReadDir(sessions)
	if err != nil {
		return claudeSessionFile{}, false
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(sessions, entry.Name()))
		if err != nil {
			continue
		}
		var sf struct {
			claudeSessionFile
			Cwd  string `json:"cwd"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &sf) != nil || sf.PID <= 1 || entry.Name() != strconv.Itoa(sf.PID)+".json" || sf.SessionID == "" || sf.Kind != "bg" || (sf.JobID == "" || (ref != "" && sf.JobID != ref)) || sf.Name != "agent-net-cto" || !sameCTODir(sf.Cwd, dir) {
			continue
		}
		if _, alive := procToken(sf.PID); alive {
			return sf.claudeSessionFile, true
		}
	}
	return claudeSessionFile{}, false
}

// ownerClaudeRenderer is the "tui" setting (the terminal renderer) from the
// owner's Claude Code user settings, or "" when there is none. The CTO starts
// with --setting-sources "", so none of the owner's settings reach it. This one
// is passed on in its launch settings: "fullscreen" is what gives every other
// session scrollback, and without it a long CTO reply runs off the screen. No
// other owner setting is read.
func ownerClaudeRenderer(sessionsDir string) string {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sessionsDir), "settings.json"))
	if err != nil {
		return ""
	}
	var user struct {
		TUI string `json:"tui"`
	}
	if json.Unmarshal(raw, &user) != nil {
		return ""
	}
	switch user.TUI {
	case "fullscreen", "default":
		return user.TUI
	}
	return ""
}

// startClaudeCTO is serialized with CTO mutations by ctoDispatchMu. Only
// inspecting and publishing runtime state takes cto.mu. Launching Claude,
// capability checks and waiting for its session file run without it.
const claudeCTORestartNotice = "The CTO's Claude conversation was closed by Claude Code; started a new one."

type claudeCTOJobFailure struct {
	detail      string
	recoverable bool
}

func (e *claudeCTOJobFailure) Error() string { return "CTO Claude job failed: " + e.detail }

func recoverableClaudeResumeFailure(status, detail string) bool {
	lower := strings.ToLower(detail)
	return status == "crashed" || strings.Contains(lower, "crash") || (strings.Contains(lower, "source session") && strings.Contains(lower, "not found"))
}

func (d *Daemon) startClaudeCTO(ctx context.Context, id, model string) error {
	d.setClaudeCTORestarted(false)
	start := func(id string) error {
		err := d.startClaudeCTOOnce(ctx, id, model)
		var failure *claudeCTOJobFailure
		if errors.As(err, &failure) {
			d.Logf("cto.crash harness=claude reason=startup_job_failed detail=%q", failure.detail)
		}
		return err
	}
	err := start(id)
	var failure *claudeCTOJobFailure
	if id == "" || !errors.As(err, &failure) || !failure.recoverable {
		return err
	}
	if err = start(""); err != nil {
		return err
	}
	d.setClaudeCTORestarted(true)
	if d.log != nil {
		d.log.Print(claudeCTORestartNotice)
	}
	return nil
}

// setClaudeCTORestarted takes cto.mu, which guards restarted: cto_enter reads
// it while a start may still be running.
func (d *Daemon) setClaudeCTORestarted(v bool) {
	d.cto.mu.Lock()
	d.claudeCTO.restarted = v
	d.cto.mu.Unlock()
}

func (d *Daemon) startClaudeCTOOnce(ctx context.Context, id, model string) error {
	d.cto.mu.Lock()
	c := &d.claudeCTO
	dir := filepath.Join(d.cfg.StateDir, "cto", "claude")
	canAdopt := c.ref == ""
	if c.ref != "" {
		sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, c.ref)
		root := c.root.Load()
		tok, alive := "", false
		if root != nil {
			tok, alive = procToken(root.pid)
		}
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, c.ref)
		if ok && !claudeJobEnded(status) && root != nil && alive && tok == c.token && sf.PID == root.pid && c.session == id && c.model == model {
			d.cto.mu.Unlock()
			return nil
		}
		reason := "configuration_change"
		if !alive || root == nil || tok != c.token || claudeJobEnded(status) {
			reason = claudeCTOEndReason(status)
		}
		d.stopClaudeCTOLocked(reason)
	}
	d.cto.mu.Unlock()
	// A daemon restart loses the in-memory job ref, but the private background
	// session may still be alive. Bind it before launching another copy.
	if sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ""); canAdopt && ok && func() bool {
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, sf.JobID)
		return !claudeJobEnded(status)
	}() && d.bindClaudeCTO(sf, model, id, "adopted") {
		return nil
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return err
	}
	if err := d.checkClaudeCTOCapabilities(ctx, bin); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := claudetrust.Set(dir); err != nil {
		return fmt.Errorf("trust CTO Claude directory: %w", err)
	}
	// CLAUDE_CODE_DISABLE_MOUSE is read inside the session, from its settings env
	// and from the environment of Claude's background service. The command that
	// starts the job and the attach client are ignored, so "0" here is the only
	// way to keep the wheel working for a long CTO reply whatever started the
	// service. See docs/decisions/0128-cto-keeps-mouse-tracking-on.md.
	// The CTO's message tool is allowed, so Claude Code's auto-mode check does not
	// block it. The name is the cto-mcp server's tool: mcp__<server>__<tool>.
	launch := map[string]any{"crossSessionInbound": "accept", "disableAllHooks": true, "enabledPlugins": map[string]bool{}, "permissions": map[string]any{"allow": []string{"mcp__agent-net-cto__agent_message_send"}}, "env": map[string]string{"AGENT_NET_CTO": "1", "AGENT_NET_SOCKET": d.cfg.SocketPath, "AGENT_NET_WORKER": "", "CLAUDE_CODE_DISABLE_MOUSE": "0", "DISABLE_AUTOUPDATER": "1"}}
	if tui := ownerClaudeRenderer(d.cfg.ClaudeSessionsDir); tui != "" {
		launch["tui"] = tui
	}
	settings, _ := json.Marshal(launch)
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"agent-net-cto": map[string]any{"command": d.cfg.Exe, "args": []string{"cto-mcp"}}}})
	spec := claudeBackground{Name: "agent-net-cto", Settings: string(settings), Resume: id,
		Flags: []string{"--tools", "", "--setting-sources", "", "--system-prompt", d.ctoSystemPrompt(ctx), "--mcp-config", string(mcp), "--strict-mcp-config"}}
	if model != "" {
		spec.ModelArgs = []string{"--model", model}
	}
	// The CTO keeps its own job tracking (private directory, crash detail, a
	// restart without the old conversation), so it runs the command itself;
	// the arguments still come from the one builder.
	args := claudeBackgroundArgs(spec)
	c.starting.Store(true)
	defer c.starting.Store(false)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// The first launch can start Claude's background service, and every session
	// that service starts later inherits this environment. Do not hand it a
	// variable that turns the wheel off.
	cmd.Env = claudeLaunchEnv(withoutEnvKey(os.Environ(), "CLAUDE_CODE_DISABLE_MOUSE"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		if claudeTrustPrompt(output) {
			return fmt.Errorf("Claude Code does not trust %s", dir)
		}
		detail := claudeCTOErrorOutput(output)
		if detail != "" {
			if id != "" && recoverableClaudeResumeFailure("", detail) {
				return &claudeCTOJobFailure{detail: detail, recoverable: true}
			}
			return fmt.Errorf("start CTO Claude: %s", detail)
		}
		return fmt.Errorf("start CTO Claude: %w", err)
	}
	m := claudeBgID.FindStringSubmatch(string(output))
	if m == nil {
		return errors.New("Claude did not return a background session id")
	}
	ref := m[1]
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ref)
		status, detail := claudeCTOJobState(d.cfg.ClaudeSessionsDir, ref)
		if claudeJobEnded(status) {
			(claudeLauncher{}).stopID(d, ref)
			lower := strings.ToLower(detail)
			return &claudeCTOJobFailure{detail: detail, recoverable: status == "crashed" || strings.Contains(lower, "crash") || (strings.Contains(lower, "source session") && strings.Contains(lower, "not found"))}
		}
		if ok && !claudeJobEnded(status) && d.bindClaudeCTO(sf, model, id, "started") {
			return nil
		}
		select {
		case <-startup.Done():
			(claudeLauncher{}).stopID(d, ref)
			return fmt.Errorf("CTO Claude session did not start: %w", startup.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// withoutEnvKey returns env without any entry for key.
func withoutEnvKey(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != key {
			out = append(out, kv)
		}
	}
	return out
}

// Terminal failure states are authoritative: a session file can appear before a resume fails.
// Claude Code writes {"state": ..., "detail": ...}; older builds used status
// and error, so both are read.
func claudeCTOJobState(sessions, ref string) (string, string) {
	if filepath.Base(ref) != ref {
		return "", ""
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sessions), "jobs", ref, "state.json"))
	if err != nil {
		return "", ""
	}
	var state struct {
		State  string          `json:"state"`
		Detail json.RawMessage `json:"detail"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return "", ""
	}
	status := state.State
	if status == "" {
		status = state.Status
	}
	detail := strings.Trim(string(state.Detail), `"`)
	if detail == "" || detail == "null" {
		detail = strings.Trim(string(state.Error), `"`)
	}
	if detail == "" || detail == "null" {
		detail = status
	}
	return strings.ToLower(status), claudeCTOErrorOutput([]byte(detail))
}

// claudeJobEnded reports a job state that means the background job is gone.
// A live job can be done between turns. For that and unknown states, the
// process check decides whether it is still alive.
func claudeJobEnded(state string) bool {
	switch state {
	case "failed", "crashed", "killed", "stopped":
		return true
	}
	return false
}

var claudeVersionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\b`)

func (d *Daemon) checkClaudeCTOCapabilities(ctx context.Context, bin string) error {
	c := &d.claudeCTO
	c.capMu.Lock()
	defer c.capMu.Unlock()
	info, err := os.Stat(bin)
	if err != nil {
		return fmt.Errorf("check Claude Code: %w", err)
	}
	if cached, ok := c.capabilities[bin]; ok && cached.mtime.Equal(info.ModTime()) {
		return cached.err
	}
	help, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("check Claude Code help: %w", err)
	}
	version, versionErr := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	v := claudeVersionPattern.FindString(string(version))
	if versionErr != nil || v == "" {
		v = "(unknown)"
	}
	if d.log != nil {
		d.log.Printf("CTO Claude Code version: %s", v)
	}
	var result error
	for _, flag := range []string{"--tools", "--strict-mcp-config"} {
		present := false
		for _, word := range strings.Fields(string(help)) {
			if strings.TrimSuffix(word, ",") == flag {
				present = true
				break
			}
		}
		if !present {
			result = fmt.Errorf("CTO Claude: required capability %s is missing from --help; run `claude update`", flag)
			break
		}
	}

	if c.capabilities == nil {
		c.capabilities = make(map[string]claudeCTOCapability)
	}
	c.capabilities[bin] = claudeCTOCapability{mtime: info.ModTime(), err: result}
	return result
}

func claudeTrustPrompt(output []byte) bool {
	text := strings.ToLower(string(output))
	return strings.Contains(text, "trust") && (strings.Contains(text, "workspace") || strings.Contains(text, "project") || strings.Contains(text, "folder"))
}

func claudeCTOErrorOutput(output []byte) string {
	lines := strings.Split(string(output), "\n")
	if len(lines) > 2 {
		lines = lines[:2]
	}
	for i, line := range lines {
		lines[i] = redact.Args(strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, line)))
	}
	return redact.Cut(redact.Secrets(strings.TrimSpace(strings.Join(lines, " · "))), 500)
}

func (claudeCTOHarness) send(ctx context.Context, d *Daemon, text string) (any, error) {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return nil, err
	}
	model := settings.ClaudeModel
	if model == "" {
		model = "opus"
	}
	id, err := d.claudeCTOSession()
	if err != nil {
		return nil, err
	}
	if err = d.startClaudeCTO(ctx, id, model); err != nil {
		return nil, err
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	oldID := id
	id = d.claudeCTO.session
	if _, err = d.st.db.Exec(`INSERT INTO cto(id,session_id,claude_session_id) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET claude_session_id=excluded.claude_session_id`, id); err != nil {
		return nil, err
	}
	if _, err = d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE (session_id='' OR session_id=?) AND harness='claude'`, id, oldID); err != nil {
		return nil, err
	}
	a, ok := d.adapters["claude"].(interface {
		DeliverCTO(context.Context, protocol.SessionInfo, string, string) Result
	})
	if !ok {
		return nil, errors.New("Claude adapter is not available")
	}
	msgID := protocol.NewToken()
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		res := a.DeliverCTO(deadline, protocol.SessionInfo{Harness: "claude", NativeID: id}, msgID, text)
		if res.Delivered {
			return map[string]string{"session_id": id}, nil
		}
		select {
		case <-deadline.Done():
			return nil, fmt.Errorf("send to CTO Claude: %s", res.Detail)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (d *Daemon) claudeCTOSession() (string, error) {
	var id string
	err := d.st.db.QueryRow(`SELECT claude_session_id FROM cto WHERE id=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (claudeCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	data, err := d.call(ctx, "cto.graph_latest", nil)
	if err != nil {
		return nil, err
	}
	var latest struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(data, &latest); err != nil {
		return nil, err
	}
	d.stopClaudeCTOLocked("new_conversation")
	tx, err := d.st.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM cto_actions WHERE session_id='' AND harness='claude'`); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO cto(id,session_id,claude_session_id,claude_graph_after) VALUES(1,'','',?) ON CONFLICT(id) DO UPDATE SET claude_session_id='',claude_graph_after=excluded.claude_graph_after`, latest.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

func (claudeCTOHarness) events(ctx context.Context, d *Daemon, index int) (protocol.CTOEvents, error) {
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	out := protocol.CTOEvents{Messages: []json.RawMessage{}, Actions: []protocol.CTOAction{}}
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return out, err
	}
	out.Model = settings.ClaudeModel
	if out.Model == "" {
		out.Model = "opus"
	}
	id, err := d.claudeCTOSession()
	if err != nil {
		return out, err
	}
	if err = d.st.db.QueryRow(`SELECT claude_graph_after FROM cto WHERE id=1`).Scan(&out.GraphAfter); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := d.st.db.Query(`SELECT id,text,at FROM cto_actions WHERE session_id=? AND harness='claude' ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a protocol.CTOAction
		if err = rows.Scan(&a.ID, &a.Text, &a.At); err != nil {
			rows.Close()
			return out, err
		}
		out.Actions = append(out.Actions, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if id == "" {
		return out, nil
	}
	messages, err := d.ctoTranscript.read(filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects"), id)
	if err != nil {
		return out, err
	}
	out.Next = len(messages)
	if index >= 0 && index < len(messages) {
		out.Messages = messages[index:]
	}
	return out, nil
}

// claudeCTOTranscript reads the whole CTO transcript from the start. The daemon
// uses a ctoTranscriptCache instead, which gives the same answer without
// parsing the lines it has already seen.
func claudeCTOTranscript(projects, id string) ([]json.RawMessage, error) {
	return new(ctoTranscriptCache).read(projects, id)
}

// ctoTranscriptCache keeps the parsed messages of the CTO transcript. The file
// is only appended to, so a call parses only the lines added since the last
// one. It starts over when the file is another file, got shorter, or no longer
// has the bytes it had at the end of the last complete line. It looks at the
// file and never at the clock: the answer is what a full parse gives.
type ctoTranscriptCache struct {
	mu       sync.Mutex
	path     string
	info     os.FileInfo
	offset   int64  // end of the last complete line that was parsed
	tail     []byte // the bytes just before offset
	lastCmd  string // command markup state at offset
	messages []json.RawMessage
	parsed   int // lines parsed so far, for tests
}

// ctoTranscriptTailCheck is how many bytes before the offset are compared to
// tell an appended file from a rewritten one.
const ctoTranscriptTailCheck = 256

func (c *ctoTranscriptCache) reset() {
	c.path, c.info, c.offset, c.tail, c.lastCmd, c.messages = "", nil, 0, nil, "", nil
}

// sameTail tells whether the file still holds the bytes the cache saw before
// its offset.
func (c *ctoTranscriptCache) sameTail(f *os.File) bool {
	if len(c.tail) == 0 {
		return c.offset == 0
	}
	buf := make([]byte, len(c.tail))
	_, err := f.ReadAt(buf, c.offset-int64(len(buf)))
	return err == nil && bytes.Equal(buf, c.tail)
}

func (c *ctoTranscriptCache) read(projects, id string) ([]json.RawMessage, error) {
	empty := []json.RawMessage{}
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, "*?[]") {
		return empty, errors.New("invalid Claude session id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var f *os.File
	var err error
	if c.path != "" && filepath.Dir(filepath.Dir(c.path)) == projects && filepath.Base(c.path) == id+".jsonl" {
		f, err = os.Open(c.path)
	}
	if f == nil || err != nil {
		matches, gerr := filepath.Glob(filepath.Join(projects, "*", id+".jsonl"))
		if gerr != nil || len(matches) == 0 {
			c.reset()
			return empty, gerr
		}
		if f, err = os.Open(matches[0]); err != nil {
			c.reset()
			return empty, err
		}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		c.reset()
		return empty, err
	}
	if c.info == nil || !os.SameFile(c.info, info) || info.Size() < c.offset || !c.sameTail(f) {
		c.reset()
	}
	c.path, c.info = f.Name(), info
	if info.Size() == c.offset {
		return c.messages[:len(c.messages):len(c.messages)], nil
	}

	// Lines that end in a newline are kept. A last line without one may still
	// be written to: it is part of this answer, but not of what is kept.
	lastCmd := c.lastCmd
	var fresh []json.RawMessage
	var consumed int64
	partial := false
	sc := bufio.NewScanner(io.NewSectionReader(f, c.offset, info.Size()-c.offset))
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		if token != nil {
			partial = advance == len(data) && data[len(data)-1] != '\n'
			if !partial {
				consumed += int64(advance)
			}
		}
		return advance, token, err
	})
	keptLen, keptCmd := 0, lastCmd
	for sc.Scan() {
		c.parsed++
		if m, ok := parseCTOTranscriptLine(sc.Bytes(), &lastCmd); ok {
			fresh = append(fresh, m)
		}
		if !partial {
			keptLen, keptCmd = len(fresh), lastCmd
		}
	}
	if err := sc.Err(); err != nil {
		// Same as a full parse: what was read, and the error.
		out := append(c.messages[:len(c.messages):len(c.messages)], fresh...)
		c.reset()
		return out, err
	}
	out := append(c.messages[:len(c.messages):len(c.messages)], fresh...)
	c.messages = append(c.messages, fresh[:keptLen]...)
	c.offset += consumed
	c.lastCmd = keptCmd
	c.tail = c.tail[:0]
	if c.offset > 0 {
		n := min(int64(ctoTranscriptTailCheck), c.offset)
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, c.offset-n); err != nil {
			c.reset()
			return out, nil
		}
		c.tail = buf
	}
	return out, nil
}

// parseCTOTranscriptLine turns one transcript line into a timeline message.
// lastCmd carries the command markup state from line to line.
func parseCTOTranscriptLine(raw []byte, lastCmd *string) (json.RawMessage, bool) {
	var line struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Side      bool   `json:"isSidechain"`
		Meta      bool   `json:"isMeta"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &line) != nil || line.Side || line.Meta || (line.Type != "user" && line.Type != "assistant") {
		return nil, false
	}
	var text string
	if json.Unmarshal(line.Message.Content, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &parts) != nil {
			return nil, false
		}
		for _, p := range parts {
			if p.Type == "text" {
				text += p.Text
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	if line.Type == "user" && strings.HasPrefix(text, "<cross-session-message ") {
		if _, body, ok := strings.Cut(text, ">\n"); ok {
			if owner, _, ok := strings.Cut(body, "\n\n[agent-net]"); ok {
				text = owner
			}
		}
	}
	clean, newCmd, _ := claudetext.CleanCommandMarkup(text, *lastCmd)
	if newCmd != "" {
		*lastCmd = newCmd
	}
	text = clean
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	var millis int64
	if at, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
		millis = at.UnixMilli()
	}
	b, _ := json.Marshal(map[string]any{"type": line.Type, "text": text, "time": map[string]int64{"created": millis}})
	return b, true
}

func (d *Daemon) bindClaudeCTO(sf claudeSessionFile, model, id, mode string) bool {
	token, alive := procToken(sf.PID)
	if !alive {
		return false
	}
	d.cto.mu.Lock()
	if mode == "adopted" && d.claudeCTO.root.Load() != nil {
		d.cto.mu.Unlock()
		return true
	}
	d.claudeCTO.ref, d.claudeCTO.session, d.claudeCTO.model, d.claudeCTO.token = sf.JobID, sf.SessionID, model, token
	d.claudeCTO.root.Store(&claudeCTORoot{pid: sf.PID, token: token})
	d.cto.mu.Unlock()
	d.Logf("cto.start harness=claude mode=%s", mode)
	d.scheduleCTOPresence()
	if d.ctoCtx != nil {
		d.goBackground(func() { d.monitorClaudeCTO(d.ctoCtx, sf.JobID, sf.PID, token) })
	}
	if id != "" && id != sf.SessionID && d.log != nil {
		d.log.Printf("CTO Claude resumed as a copy: %s -> %s", id, sf.SessionID)
	}
	return true
}

// adoptLiveClaudeCTO restores the process root before reconnecting MCP children.
func (d *Daemon) adoptLiveClaudeCTO() bool {
	if d.claudeCTO.starting.Load() {
		return false
	}
	d.cto.mu.Lock()
	bound := d.claudeCTO.root.Load() != nil
	d.cto.mu.Unlock()
	if bound {
		return false
	}
	sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, filepath.Join(d.cfg.StateDir, "cto", "claude"), "")
	if !ok {
		return false
	}
	status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, sf.JobID)
	if claudeJobEnded(status) {
		return false
	}
	_, model, _, err := d.localCTOModelSettings()
	if err != nil {
		return false
	}
	if model == "" {
		model = "opus"
	}
	return d.bindClaudeCTO(sf, model, "", "adopted")
}
