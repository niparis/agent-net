package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// claudeCTOState runtime fields are protected by cto.mu. The process root and
// starting flag are atomic; the capability cache is serialized by ctoDispatchMu.
type claudeCTOState struct {
	root                atomic.Pointer[claudeCTORoot]
	starting            atomic.Bool
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

func (d *Daemon) stopClaudeCTOLocked() {
	c := &d.claudeCTO
	root := c.root.Load()
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
	d.claudeCTO.restarted = false
	err := d.startClaudeCTOOnce(ctx, id, model)
	var failure *claudeCTOJobFailure
	if id == "" || !errors.As(err, &failure) || !failure.recoverable {
		return err
	}
	if err = d.startClaudeCTOOnce(ctx, "", model); err != nil {
		return err
	}
	d.claudeCTO.restarted = true
	if d.log != nil {
		d.log.Print(claudeCTORestartNotice)
	}
	return nil
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
		if ok && status == "running" && root != nil && alive && tok == c.token && sf.PID == root.pid && c.session == id && c.model == model {
			d.cto.mu.Unlock()
			return nil
		}
		d.stopClaudeCTOLocked()
	}
	d.cto.mu.Unlock()
	adopt := func(sf claudeSessionFile) bool {
		token, alive := procToken(sf.PID)
		if !alive {
			return false
		}
		d.cto.mu.Lock()
		c.ref, c.session, c.model, c.token = sf.JobID, sf.SessionID, model, token
		c.root.Store(&claudeCTORoot{pid: sf.PID, token: token})
		d.cto.mu.Unlock()
		if id != "" && id != sf.SessionID && d.log != nil {
			d.log.Printf("CTO Claude resumed as a copy: %s -> %s", id, sf.SessionID)
		}
		return true
	}
	// A daemon restart loses the in-memory job ref, but the private background
	// session may still be alive. Bind it before launching another copy.
	if sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, ""); canAdopt && ok && func() bool {
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, sf.JobID)
		return status == "running"
	}() && adopt(sf) {
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
	settings, _ := json.Marshal(map[string]any{"crossSessionInbound": "accept", "disableAllHooks": true, "enabledPlugins": map[string]bool{}, "env": map[string]string{"AGENT_NET_CTO": "1", "AGENT_NET_SOCKET": d.cfg.SocketPath, "AGENT_NET_WORKER": "", "CLAUDE_CODE_DISABLE_MOUSE": "1"}})
	args := []string{"--bg", "--tools", "", "--setting-sources", "", "--name", "agent-net-cto", "--settings", string(settings), "--system-prompt", ctoBrief}
	if id != "" {
		args = append(args, "--resume", id)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"agent-net-cto": map[string]any{"command": d.cfg.Exe, "args": []string{"cto-mcp"}}}})
	args = append(args, "--mcp-config", string(mcp), "--strict-mcp-config")
	c.starting.Store(true)
	defer c.starting.Store(false)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = workerEnv(os.Environ(), map[string]string{"CLAUDE_CODE_DISABLE_MOUSE": "1"})
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
		if status == "failed" || status == "crashed" || status == "killed" {
			(claudeLauncher{}).stopID(d, ref)
			lower := strings.ToLower(detail)
			return &claudeCTOJobFailure{detail: detail, recoverable: status == "crashed" || strings.Contains(lower, "crash") || (strings.Contains(lower, "source session") && strings.Contains(lower, "not found"))}
		}
		if ok && status == "running" && adopt(sf) {
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

// Job state is authoritative: a session file can appear before a resume fails.
func claudeCTOJobState(sessions, ref string) (string, string) {
	if filepath.Base(ref) != ref {
		return "", ""
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sessions), "jobs", ref, "state.json"))
	if err != nil {
		return "", ""
	}
	var state struct {
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return "", ""
	}
	detail := strings.Trim(string(state.Error), `"`)
	if detail == "" || detail == "null" {
		detail = state.Status
	}
	return strings.ToLower(state.Status), claudeCTOErrorOutput([]byte(detail))
}

var claudeVersionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\b`)

func (d *Daemon) checkClaudeCTOCapabilities(ctx context.Context, bin string) error {
	info, err := os.Stat(bin)
	if err != nil {
		return fmt.Errorf("check Claude Code: %w", err)
	}
	c := &d.claudeCTO
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
	d.stopClaudeCTOLocked()
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
	messages, err := claudeCTOTranscript(filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects"), id)
	if err != nil {
		return out, err
	}
	out.Next = len(messages)
	if index >= 0 && index < len(messages) {
		out.Messages = messages[index:]
	}
	return out, nil
}

func claudeCTOTranscript(projects, id string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, "*?[]") {
		return out, errors.New("invalid Claude session id")
	}
	matches, err := filepath.Glob(filepath.Join(projects, "*", id+".jsonl"))
	if err != nil || len(matches) == 0 {
		return out, err
	}
	f, err := os.Open(matches[0])
	if err != nil {
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	for sc.Scan() {
		var line struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Side      bool   `json:"isSidechain"`
			Meta      bool   `json:"isMeta"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Side || line.Meta || (line.Type != "user" && line.Type != "assistant") {
			continue
		}
		var text string
		if json.Unmarshal(line.Message.Content, &text) != nil {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(line.Message.Content, &parts) != nil {
				continue
			}
			for _, p := range parts {
				if p.Type == "text" {
					text += p.Text
				}
			}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if line.Type == "user" && strings.HasPrefix(text, "<cross-session-message ") {
			if _, body, ok := strings.Cut(text, ">\n"); ok {
				if owner, _, ok := strings.Cut(body, "\n\n[agent-net]"); ok {
					text = owner
				}
			}
		}
		var millis int64
		if at, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
			millis = at.UnixMilli()
		}
		b, _ := json.Marshal(map[string]any{"type": line.Type, "text": text, "time": map[string]int64{"created": millis}})
		out = append(out, b)
	}
	return out, sc.Err()
}
