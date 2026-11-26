package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// Antigravity and Codex hand back after the model's turn has ended: agy after
// its agent_handback call was acknowledged, Codex from the turn's last
// message. A refusal there is no tool error that the model sees, so the daemon
// tells the caller itself: the worker's report, and why agent-net could not
// take it. A hand-back refused for an ordinary tool call is returned to the
// worker, which can correct it and hand back again.

// handbackRefusedStatus is the status line of the caller's notice.
const handbackRefusedStatus = "hand-back refused"

// tellCallerRefused sends the caller the worker's report and the reason. It
// says whether the notice went out.
func (d *Daemon) tellCallerRefused(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, reason string) bool {
	d.wmu.Lock()
	stopping := w.stopping
	d.wmu.Unlock()
	status := req.Status
	if status == "" {
		status = protocol.StatusDone
	}
	note := "agent-net could not take the worker's hand-back (status " + status + "): " + reason + "; the report below is the worker's own"
	if !stopping {
		note += "; the worker is still running, and a message from you continues it"
	}
	n := d.notice(w, handbackRefusedStatus, note, d.workerStats(w))
	if _, err := d.sendNotice(ctx, w, n, req.Report); err != nil {
		d.Logf("worker %s: refused hand-back notice: %v", w.id, err)
		return false
	}
	return true
}

// codexRefused handles a refused Codex hand-back: the caller gets the report
// and the reason, and the idle period marks the turn handled so adoption does
// not hand it back again. When the notice cannot go out, the idle notice
// carries the report instead.
func (d *Daemon) codexRefused(w *worker, req protocol.WorkerHandbackRequest, final string, err error) {
	if d.tellCallerRefused(context.Background(), w, req, cleanFailure(err.Error())) {
		d.noteCodexIdle(w, final, true)
		return
	}
	d.noteCodexIdle(w, final, false)
}
