package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Antigravity and Codex hand back after the model's turn has ended: agy after
// its agent_handback call was acknowledged, Codex from the turn's last
// message. A refusal there is no tool error that the model sees, so the
// daemon tells someone itself. A cause the worker can correct goes back to
// the worker once, as a message in its own session. Any other cause, or a
// second refusal, goes to the caller with the worker's report and the reason.
// Until one of them has it, the report is kept in the worker's folder, so a
// daemon restart does not lose it.

// handbackRefusedStatus is the status line of the caller's notice.
const handbackRefusedStatus = "hand-back refused"

// handbackFixable marks a refusal the worker can correct by handing back
// again: the request itself is wrong, not agent-net or the gateway.
type handbackFixable struct{ error }

func (e handbackFixable) Unwrap() error { return e.error }

// refusedHandback is the saved form of a refused hand-back.
type refusedHandback struct {
	Req    protocol.WorkerHandbackRequest `json:"req"`
	Reason string                         `json:"reason"`
	// Told is true once the worker had its one message about a refusal.
	Told bool `json:"told,omitempty"`
}

func (d *Daemon) refusedHandbackPath(workerID string) string {
	return filepath.Join(d.cfg.StateDir, "workers", workerID, "handback-refused.json")
}

func (d *Daemon) loadRefusedHandback(workerID string) (refusedHandback, bool) {
	var r refusedHandback
	b, err := os.ReadFile(d.refusedHandbackPath(workerID))
	if err != nil || json.Unmarshal(b, &r) != nil {
		return refusedHandback{}, false
	}
	return r, true
}

func (d *Daemon) saveRefusedHandback(workerID string, r refusedHandback) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := d.refusedHandbackPath(workerID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (d *Daemon) dropRefusedHandback(workerID string) {
	if err := os.Remove(d.refusedHandbackPath(workerID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		d.Logf("worker %s: remove the refused hand-back: %v", workerID, err)
	}
}

// handbackRefused tells the worker or its caller about a hand-back that
// handbackFlow refused after the turn. release runs once the report is saved
// and before anyone is told: agy frees its turn there. It reports whether
// someone was told, or the hand-back is saved for the next connect.
func (d *Daemon) handbackRefused(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, err error, release func()) bool {
	if errors.Is(err, errHandbackDeferred) {
		return true
	}
	r, _ := d.loadRefusedHandback(w.id)
	r.Req, r.Reason = req, cleanFailure(err.Error())
	var fixable handbackFixable
	tellWorker := errors.As(err, &fixable) && !r.Told
	r.Told = r.Told || tellWorker
	if err := d.saveRefusedHandback(w.id, r); err != nil {
		d.Logf("worker %s: save the refused hand-back: %v", w.id, err)
	}
	if release != nil {
		release()
	}
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	d.Logf("worker.handback_refused worker=%s session=%s tell_worker=%t reason=%q", w.id, sid, tellWorker, r.Reason)
	if tellWorker {
		err := d.tellWorkerRefused(ctx, w, r.Reason)
		if err == nil {
			return true
		}
		d.Logf("worker %s: refused hand-back message: %v", w.id, err)
	}
	return d.tellCallerRefused(ctx, w, r)
}

// tellWorkerRefused sends the worker its one message about a refusal it can
// correct, through the same path as an automatic continue.
func (d *Daemon) tellWorkerRefused(ctx context.Context, w *worker, reason string) error {
	d.wmu.Lock()
	sid, harness := w.sessionID, w.harness
	d.wmu.Unlock()
	m := protocol.Message{ID: protocol.NewID("m"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: sid, Type: protocol.TypeNotification, Body: handbackRefusedText(harness, reason)}
	return d.deliverAutoContinue(ctx, w, m)
}

func handbackRefusedText(harness, reason string) string {
	again := "Fix the cause and call agent_handback again. If you cannot fix it, hand back with status blocked and say why."
	if harness == "codex" {
		again = "Fix the cause and end your turn with your final report again. If you cannot fix it, say why in that report."
	}
	return "agent-net could not take your hand-back: " + reason + "\nYour report is kept. " + again +
		"\nThis is the only message about it: if your next hand-back is refused too, your caller gets your report and the reason."
}

// tellCallerRefused sends the caller the worker's report and the reason. The
// saved report is removed once the notice is sent.
func (d *Daemon) tellCallerRefused(ctx context.Context, w *worker, r refusedHandback) bool {
	d.wmu.Lock()
	stopping := w.stopping
	d.wmu.Unlock()
	status := r.Req.Status
	if status == "" {
		status = protocol.StatusDone
	}
	note := "agent-net could not take the worker's hand-back (status " + status + "): " + r.Reason + "; the report below is the worker's own"
	if !stopping {
		note += "; the worker is still running, and a message from you continues it"
	}
	n := d.notice(w, handbackRefusedStatus, note, d.workerStats(w))
	if _, err := d.sendNoticeToKey(ctx, w, n, r.Req.Report, "", "", "", false); err != nil {
		d.Logf("worker %s: refused hand-back notice: %v", w.id, err)
		return false
	}
	d.dropRefusedHandback(w.id)
	return true
}

// refusedReportFor puts a refused hand-back that nobody has yet into the
// notice of the worker's end, so the report is never lost. It returns the
// report to send below the facts, or "".
func (d *Daemon) refusedReportFor(workerID string, n *protocol.WorkerNotice) string {
	r, ok := d.loadRefusedHandback(workerID)
	if !ok {
		return ""
	}
	n.Note = strings.TrimPrefix(n.Note+"; its last hand-back was refused: "+r.Reason+"; the report below is the worker's own", "; ")
	return r.Req.Report
}
