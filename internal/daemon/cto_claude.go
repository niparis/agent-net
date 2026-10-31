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

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// claudeCTOState is protected by cto.mu, except pid, which is read by the
// private-channel peer check without taking that lock.
type claudeCTOState struct {
	root                atomic.Pointer[claudeCTORoot]
	starting            atomic.Bool
	token               string
	ref, session, model string
	capabilities        map[string]claudeCTOCapability
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
// working directory. The PID file name must match the session's PID.
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
		if json.Unmarshal(raw, &sf) != nil || sf.PID <= 1 || entry.Name() != strconv.Itoa(sf.PID)+".json" || sf.SessionID == "" || sf.Kind != "bg" || sf.JobID != ref || sf.Name != "agent-net-cto" || !sameCTODir(sf.Cwd, dir) {
			continue
		}
		if _, alive := procToken(sf.PID); alive {
			return sf.claudeSessionFile, true
		}
	}
	return claudeSessionFile{}, false
}

func (d *Daemon) startClaudeCTOLocked(ctx context.Context, id, model string) error {
	c := &d.claudeCTO
	dir := filepath.Join(d.cfg.StateDir, "cto", "claude")
	if c.ref != "" {
		sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, dir, c.ref)
		root := c.root.Load()
		tok, alive := "", false
		if root != nil {
			tok, alive = procToken(root.pid)
		}
		if ok && root != nil && alive && tok == c.token && sf.PID == root.pid && c.session == id && c.model == model {
			return nil
		}
		d.stopClaudeCTOLocked()
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
	settings, _ := json.Marshal(map[string]any{"crossSessionInbound": "accept", "env": map[string]string{"AGENT_NET_CTO": "1", "AGENT_NET_SOCKET": d.cfg.SocketPath, "AGENT_NET_WORKER": ""}})
	args := []string{"--bg", "--name", "agent-net-cto", "--settings", string(settings), "--system-prompt", ctoBrief}
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
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := claudeCTOErrorOutput(output)
		if detail != "" {
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
		if ok {
			token, alive := procToken(sf.PID)
			if alive && (id == "" || sf.SessionID == id) {
				c.ref, c.session, c.model, c.token = ref, sf.SessionID, model, token
				c.root.Store(&claudeCTORoot{pid: sf.PID, token: token})
				return nil
			}
		}
		select {
		case <-startup.Done():
			(claudeLauncher{}).stopID(d, ref)
			return fmt.Errorf("CTO Claude session did not start: %w", startup.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
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
	var result error
	if !strings.Contains(string(help), "--bg") || !strings.Contains(string(help), "--strict-mcp-config") {
		version, versionErr := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
		if versionErr != nil {
			return fmt.Errorf("check Claude Code version: %w", versionErr)
		}
		v := claudeVersionPattern.FindString(string(version))
		if v == "" {
			v = "(unknown version)"
		}
		result = fmt.Errorf("Claude Code %s is too old for the CTO: run `claude update`", v)
	}
	if c.capabilities == nil {
		c.capabilities = make(map[string]claudeCTOCapability)
	}
	c.capabilities[bin] = claudeCTOCapability{mtime: info.ModTime(), err: result}
	return result
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
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return nil, err
	}
	model := settings.Model
	if model == "" {
		model = "opus"
	}
	id, err := d.claudeCTOSession()
	if err != nil {
		return nil, err
	}
	if err = d.startClaudeCTOLocked(ctx, id, model); err != nil {
		return nil, err
	}
	id = d.claudeCTO.session
	if _, err = d.st.db.Exec(`INSERT INTO cto(id,session_id,claude_session_id) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET claude_session_id=excluded.claude_session_id`, id); err != nil {
		return nil, err
	}
	if _, err = d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE session_id='' AND harness='claude'`, id); err != nil {
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
	out.Model = settings.Model
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
