package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

// The chief of staff starts through the ordinary session start (decision
// 0171, phase 2). Nothing here is special to a harness: the model list is the
// chief.model setting, the gate is the one of decision 0162, and the started
// session is pinned with chief_set like a hand-started one.

// chiefFirstMessage is what a started chief reads first. The rest of what it
// needs (the asks it sorts, the Desk) arrives as messages and tools.
const chiefFirstMessage = `You are the owner's chief of staff, started by agent-net. Your memory is your gateway notes: save one with agent_note_write and read them with agent_notes_find. Read your notes first, then the asks that wait for you.`

// chiefStartRetry is how long a failed chief start waits before the loop
// tries again. The reason of the failure is logged once per try.
const chiefStartRetry = 10 * time.Minute

// chiefLoopEvery is how often the chief loop looks at the chief. One read of
// the chief's status, on the machine that runs the CTO only.
const chiefLoopEvery = time.Minute

// chiefClaudeSettings is the launch settings of a Claude chief. Its memory is
// the gateway notes, which the owner sees and deletes in the TUI, so Claude
// Code's auto memory is off for it (as for the CTO, owner decision 2026-11-13).
func chiefClaudeSettings() string {
	settings, _ := json.Marshal(map[string]any{"autoMemoryEnabled": false})
	return string(settings)
}

// chiefFolder is the folder a started chief runs in. It is its own folder, so
// no harness shares a project's memory or trust with it.
func (d *Daemon) chiefFolder() string { return filepath.Join(d.cfg.StateDir, "chief") }

// isChiefSession tells whether the gateway names id as the chief. A resume uses
// it to give the chief the same launch settings as its start. An error is no.
func (d *Daemon) isChiefSession(ctx context.Context, id string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := d.chiefStatus(ctx)
	return err == nil && id != "" && st.SessionID == id
}

func (d *Daemon) chiefStatus(ctx context.Context) (protocol.ChiefStatus, error) {
	var st protocol.ChiefStatus
	raw, err := d.call(ctx, "chief.get", struct{}{})
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(raw, &st)
}

// chiefLoop starts a chief when there is none that can run. Only the machine
// that holds the CTO's identity runs it, so two machines never start two
// chiefs. A failed start is tried again after chiefStartRetry.
func (d *Daemon) chiefLoop(ctx context.Context) {
	ticker := time.NewTicker(chiefLoopEvery)
	defer ticker.Stop()
	var failedAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if d.ctoSessionID() == "" || (!failedAt.IsZero() && time.Since(failedAt) < chiefStartRetry) {
			continue
		}
		failedAt = time.Time{}
		if err := d.ensureChief(ctx); err != nil {
			failedAt = time.Now()
			d.Logf("chief.start failed: %v", err)
		}
	}
}

// ensureChief starts the chief when no chief is pinned, or when the pinned
// chief is offline on a harness that cannot be woken. A chief that is online,
// or offline on a harness a message can wake, is left to the wake path. It
// starts on the first usable entry of chief.model, through the gate, and pins
// the session it started.
func (d *Daemon) ensureChief(ctx context.Context) error {
	if !d.chiefStarting.CompareAndSwap(false, true) {
		return nil
	}
	defer d.chiefStarting.Store(false)
	st, err := d.chiefStatus(ctx)
	if err != nil {
		return err
	}
	if st.SessionID != "" {
		if st.Online {
			return nil
		}
		if a, err := d.agentView(ctx, st.SessionID); err == nil && protocol.WakeHarness(a.Harness) {
			return nil
		}
	}
	pick, err := d.pickRoleOnMachine(ctx, protocol.RoleChief, d.cfg.NodeID, nil, protocol.RoleModel{}, false)
	if err != nil {
		return fmt.Errorf("chief model: %w", err)
	}
	dir := d.chiefFolder()
	if _, err := os.Stat(dir); err == nil && pick.Harness == "claude" {
		// A folder agent-net made before is trusted for Claude only when the
		// first chief ran on Claude. Trusting it again is safe: it is ours.
		if err := claudetrust.Set(dir); err != nil {
			return fmt.Errorf("trust the chief folder: %w", err)
		}
	}
	started, err := d.sessionStart(ctx, protocol.SessionStartRequest{
		Chief: true, Path: dir, Create: true, NodeID: d.cfg.NodeID,
		Harness: pick.Harness, Model: pick.Model, FirstMessage: chiefFirstMessage,
	})
	if err != nil {
		return err
	}
	if started, err = d.waitStarted(ctx, started); err != nil {
		return err
	}
	if _, err := d.call(ctx, "chief.set", struct {
		protocol.ChiefSetRequest
		By string `json:"by"`
	}{protocol.ChiefSetRequest{SessionID: started.SessionID}, "owner"}); err != nil {
		return fmt.Errorf("pin the chief: %w", err)
	}
	d.Logf("chief.start harness=%s model=%s session=%s", pick.Harness, pick.Model, started.SessionID)
	return nil
}

// waitStarted follows a local start until it has a session ID or has failed.
func (d *Daemon) waitStarted(ctx context.Context, r protocol.StartedSession) (protocol.StartedSession, error) {
	ctx, cancel := context.WithTimeout(ctx, d.workerSettings("").StartTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch {
		case r.Status == "failed":
			return r, errors.New("the chief did not start: " + r.Error)
		case r.Status == "needs_trust" || r.Status == "needs_create":
			return r, errors.New("the chief did not start: " + r.Status)
		case r.Status == "started" && r.SessionID != "":
			return r, nil
		}
		select {
		case <-ctx.Done():
			return r, fmt.Errorf("the chief did not register: %w", ctx.Err())
		case <-ticker.C:
		}
		next, err := d.sessionStartGetLocal(r.ID)
		if err != nil {
			return r, err
		}
		r = next
	}
}
