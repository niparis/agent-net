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
	"strings"
	"sync/atomic"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// claudeCTOState is protected by cto.mu, except pid, which is read by the
// private-channel peer check without taking that lock.
type claudeCTOState struct {
	pid                 atomic.Int64
	token               string
	ref, session, model string
}

type claudeCTOHarness struct{}

func (claudeCTOHarness) name() string { return "claude" }

func (d *Daemon) stopClaudeCTOLocked() {
	c := &d.claudeCTO
	c.pid.Store(0)
	if c.ref != "" {
		(claudeLauncher{}).stopID(d, c.ref)
	}
	c.ref, c.session, c.model, c.token = "", "", "", ""
}

func (d *Daemon) startClaudeCTOLocked(ctx context.Context, id, model string) error {
	c := &d.claudeCTO
	if c.ref != "" {
		sf, ok := findClaudeBg(d.cfg.ClaudeSessionsDir, c.ref)
		tok, alive := procToken(int(c.pid.Load()))
		if ok && alive && tok == c.token && sf.PID == int(c.pid.Load()) && c.session == id && c.model == model {
			return nil
		}
		d.stopClaudeCTOLocked()
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return err
	}
	dir := filepath.Join(d.cfg.StateDir, "cto", "claude")
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
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
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
		sf, ok := findClaudeBg(d.cfg.ClaudeSessionsDir, ref)
		if ok {
			token, alive := procToken(sf.PID)
			if alive && (id == "" || sf.SessionID == id) {
				c.ref, c.session, c.model, c.token = ref, sf.SessionID, model, token
				c.pid.Store(int64(sf.PID))
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
	id, err := d.ctoSession()
	if err != nil {
		return nil, err
	}
	if err = d.startClaudeCTOLocked(ctx, id, model); err != nil {
		return nil, err
	}
	id = d.claudeCTO.session
	if _, err = d.st.db.Exec(`INSERT INTO cto(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, id); err != nil {
		return nil, err
	}
	if _, err = d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE session_id=''`, id); err != nil {
		return nil, err
	}
	a := d.adapters["claude"]
	if a == nil {
		return nil, errors.New("Claude adapter is not available")
	}
	msg := protocol.Message{ID: protocol.NewToken(), Body: text, FromHarness: protocol.OwnerHarness, FromName: "owner"}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		res := a.Deliver(deadline, protocol.SessionInfo{Harness: "claude", NativeID: id}, msg)
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

func (claudeCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	d.cto.mu.Lock()
	d.stopClaudeCTOLocked()
	d.cto.mu.Unlock()
	return d.openCodeCTONew(ctx)
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
	id, err := d.ctoSession()
	if err != nil {
		return out, err
	}
	if err = d.st.db.QueryRow(`SELECT graph_after FROM cto WHERE id=1`).Scan(&out.GraphAfter); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := d.st.db.Query(`SELECT id,text,at FROM cto_actions WHERE session_id=? ORDER BY id`, id)
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
			Type    string `json:"type"`
			Side    bool   `json:"isSidechain"`
			Meta    bool   `json:"isMeta"`
			Message struct {
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
		b, _ := json.Marshal(map[string]string{"type": line.Type, "text": text})
		out = append(out, b)
	}
	return out, sc.Err()
}
