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
	return d.wakeClaude(ctx, id, token, false)
}

// claudeAgentRecord is one entry of `claude agents --json --all`.
type claudeAgentRecord struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
	State     string `json:"state"`
}

// interactiveRecordClear tells whether one record of the session lets an
// autonomy wake go ahead, whatever the session's mode. A live record, one that
// names another session, and a background record that did not finish or is in
// another folder all stop it. A record with no kind counts as background only
// for a session recorded as background, as the retired check reads it. A
// finished background run leaves its record behind, so a finished one in the
// session's folder does not: the session's owner resumes it the same way.
func interactiveRecordClear(record claudeAgentRecord, identity bool, mode, cwd string) bool {
	if !identity || record.PID != 0 {
		return false
	}
	kind := record.Kind
	if kind == "" && mode == protocol.ModeBackground {
		kind = "background"
	}
	switch kind {
	case "interactive":
		return true
	case "background", "bg":
		return record.State == "done" && record.Cwd != "" && filepath.Clean(record.Cwd) == filepath.Clean(cwd)
	}
	return false
}

// wakeClaude is the Claude wake. A background session is resumed only from a
// retired record. With autonomy wake the gateway also asks for an interactive
// session, which is resumed in the background the same way (never a view).
func (d *Daemon) wakeClaude(ctx context.Context, id, token string, autonomy bool) error {
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
	var agents []claudeAgentRecord
	if err := json.Unmarshal(out, &agents); err != nil {
		return errors.New("cannot parse Claude background records")
	}
	matches := 0
	retired := false
	// An interactive session that is not running has no background record to
	// check, so its absence is normal. Anything live, or any record that does
	// not say plainly what it is, is not. With autonomy wake, every record of
	// the session must be clear, whatever the session's mode.
	interactive := autonomy
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
		background := record.Kind == "background" || record.Kind == "bg" || (record.Kind == "" && a.Mode == protocol.ModeBackground)
		retired = identity && background && record.PID == 0 && record.State == "done" && record.Cwd != "" && filepath.Clean(record.Cwd) == filepath.Clean(a.Cwd)
		if !interactiveRecordClear(record, identity, a.Mode, a.Cwd) {
			interactive = false
		}
	}
	if !(matches == 1 && retired) && !interactive {
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
		Autonomy  string `json:"autonomy"`
	}
	var err error
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		err = errors.New("bad session wake")
	} else {
		err = d.wakeSession(ctx, p.SessionID, p.Token, p.Autonomy == protocol.AutonomyWake)
	}
	if res, e := protocol.EncodeResult(h.ID, map[string]bool{"ok": err == nil}, err); e == nil {
		l.write(ctx, res)
	}
}
