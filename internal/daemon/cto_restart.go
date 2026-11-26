package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// A daemon stop ends a Codex or OpenCode CTO: its server is the daemon's child.
// A Claude CTO's job outlives the daemon and is adopted at start. So that every
// update does not leave a Codex or OpenCode CTO down until a message or Enter,
// the stop records which one ran (cto.running_at_stop), and the next daemon
// starts it again in the background once it is connected. The start reads and
// clears the record, so a crash, which writes none, behaves as before: the CTO
// waits for a message or Enter.

// ctoRestartHarnessLocked is the Codex or OpenCode harness that runs the CTO
// now, or "". A harness still writing its handover note is not the CTO. The
// caller holds cto.mu.
func (d *Daemon) ctoRestartHarnessLocked() string {
	if d.cto.cmd != nil && !d.ctoHandoverPendingLocked("opencode") {
		select {
		case <-d.cto.exited:
		default:
			return "opencode"
		}
	}
	if d.codexCTORunningLocked() && !d.ctoHandoverPendingLocked("codex") {
		return "codex"
	}
	return ""
}

// recordCTORunningAtStop stores the harness that ran at the daemon stop.
func (d *Daemon) recordCTORunningAtStop(harness string) {
	if _, err := d.st.db.Exec(`UPDATE cto SET running_at_stop=? WHERE id=1`, harness); err != nil {
		d.Logf("cto.stop record harness=%q: %v", harness, err)
	}
}

// planCTORestart reads and clears the record of the last stop. A CTO the owner
// stopped is not started again.
func (d *Daemon) planCTORestart() {
	var harness string
	if err := d.st.db.QueryRow(`SELECT running_at_stop FROM cto WHERE id=1`).Scan(&harness); err != nil || harness == "" {
		return
	}
	if _, err := d.st.db.Exec(`UPDATE cto SET running_at_stop='' WHERE id=1`); err != nil {
		d.Logf("cto.restart clear record: %v", err)
		return
	}
	stopped, err := d.ctoStoppedByOwner()
	if err != nil {
		d.Logf("cto.restart harness=%s: read the owner stop: %v; the CTO waits for a message or Enter", harness, err)
		return
	}
	if stopped {
		d.Logf("cto.restart skipped harness=%s: the owner stopped the CTO", harness)
		return
	}
	d.ctoRestartPlan = harness
}

// ctoStartAfterRestart is the harness this daemon starts again once
// connected, or "".
func (d *Daemon) ctoStartAfterRestart() string { return d.ctoRestartPlan }

// startCTOAfterRestart runs the planned start once, on the first connection:
// the start registers the CTO and reads its settings at the gateway.
func (d *Daemon) startCTOAfterRestart() {
	harness := d.ctoRestartPlan
	if harness == "" || !d.ctoRestartTaken.CompareAndSwap(false, true) {
		return
	}
	ctx := d.ctoCtx
	d.goBackground(func() { d.restartCTO(ctx, harness) })
}

// restartCTO starts the CTO on harness when that is still the selected
// harness. A start that fails is logged once; the CTO then waits for a message
// or Enter, as before.
func (d *Daemon) restartCTO(ctx context.Context, harness string) {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		d.Logf("cto.restart harness=%s failed: read the CTO settings: %v; the CTO waits for a message or Enter", harness, err)
		return
	}
	if settings.Harness != harness {
		d.Logf("cto.restart skipped harness=%s: the selected harness is %s", harness, settings.Harness)
		return
	}
	// The owner may have stopped the CTO since this daemon started.
	if stopped, err := d.ctoStoppedByOwner(); err != nil || stopped {
		d.Logf("cto.restart skipped harness=%s: owner stop=%t err=%v", harness, stopped, err)
		return
	}
	if _, err := d.ctoEnterFor(ctx, "daemon_restart"); err != nil {
		d.Logf("cto.restart harness=%s failed: %v; the CTO waits for a message or Enter", harness, err)
	}
}
