package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// wakeRetiredClaude accepts only a known local non-worker with an unambiguous
// matching background record in state done. The record, not gateway presence,
// is authoritative about a stop or removal by the owner.
func (d *Daemon) wakeRetiredClaude(ctx context.Context, id, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	for !d.claudeStartMu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	locked := true
	defer func() {
		if locked {
			d.claudeStartMu.Unlock()
		}
	}()
	a, err := d.agentView(ctx, id)
	if err != nil {
		return err
	}
	if a.NodeID != d.cfg.NodeID || a.Harness != "claude" || a.Worker != nil || a.NativeID == "" || a.Online {
		return errors.New("not an offline local Claude owner session")
	}
	if d.workerBySession(id) != nil {
		return errors.New("workers cannot be woken")
	}
	if _, ok, err := d.st.workerBySession(id); err != nil || ok {
		if err != nil {
			return err
		}
		return errors.New("workers cannot be woken")
	}
	info, err := os.Stat(a.Cwd)
	if err != nil || !info.IsDir() {
		return errors.New("session folder is gone")
	}
	bin, err := d.harnessBinary("claude")
	if err != nil {
		return err
	}
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(listCtx, bin, "agents", "--json", "--all").Output()
	if err != nil {
		return fmt.Errorf("cannot read Claude background records: %w", err)
	}
	var agents []struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		Kind      string `json:"kind"`
		Cwd       string `json:"cwd"`
		State     string `json:"state"`
	}
	if err := json.Unmarshal(out, &agents); err != nil {
		return errors.New("cannot parse Claude background records")
	}
	matches := 0
	retired := false
	for _, record := range agents {
		// Some Claude records carry the full session ID. When they do, it
		// must agree; otherwise use the short job ID only as a prefix.
		// Count every prefix collision, including in another folder.
		prefix := len(record.ID) >= 8 && strings.HasPrefix(a.NativeID, record.ID)
		full := record.SessionID != "" && record.SessionID == a.NativeID
		if !prefix && !full {
			continue
		}
		matches++
		identity := full || (prefix && record.SessionID == "")
		background := record.Kind == "bg" || (record.Kind == "" && a.Mode == protocol.ModeBackground)
		retired = identity && background && record.State == "done" && record.Cwd != "" && filepath.Clean(record.Cwd) == filepath.Clean(a.Cwd)
	}
	if matches != 1 || !retired {
		return errors.New("Claude background record is missing, ambiguous, or not retired")
	}
	// This is the same background launcher as owner resume, without any view.
	sf, err := d.launchClaudeBackground(ctx, bin, a.Cwd, []string{"--bg", "--resume", a.NativeID}, "", a.NativeID, nil, nil)
	if err != nil {
		return err
	}
	if sf.SessionID != a.NativeID {
		if token == "" {
			return errors.New("resumed Claude copy has no wake token")
		}
		_, err = d.call(ctx, protocol.FrameSessionWakeRebind, map[string]string{"session_id": id, "token": token, "native_id": sf.SessionID})
		if err != nil {
			return fmt.Errorf("cannot register resumed Claude copy: %w", err)
		}
	}
	// Discovery takes the same lock. Release it as soon as the launched
	// session file is known, as the normal Claude resume path does.
	d.claudeStartMu.Unlock()
	locked = false
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ls := d.sessionByID(id); ls != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("resumed Claude session did not register: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (d *Daemon) onSessionWakeFrame(ctx context.Context, l *link, h protocol.Header, raw []byte) {
	var p struct {
		SessionID string `json:"session_id"`
		Token     string `json:"token"`
	}
	var err error
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		err = errors.New("bad session wake")
	} else {
		err = d.wakeRetiredClaude(ctx, p.SessionID, p.Token)
	}
	if res, e := protocol.EncodeResult(h.ID, map[string]bool{"ok": err == nil}, err); e == nil {
		l.write(ctx, res)
	}
}
