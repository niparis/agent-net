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
//
// The refusal is saved in the worker's folder before anyone is told, and the
// saved record is what adoption reads: a daemon that stops at any step either
// publishes the hand-back again with nobody told yet, or finds the record and
// does what it still owes, once.

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
	// Turn is the agy turn whose hand-back was refused, as agyConn.turnKey
	// names it. Adoption compares it with the stream state to see whether the
	// worker's message went out.
	Turn string `json:"turn,omitempty"`
	// NoticeKey is the idempotency key of the caller's notice. A notice sent
	// again after a restart has the same key, so the gateway keeps the first.
	NoticeKey string `json:"notice_key"`
	// Worker is true when the worker gets the one message about this
	// refusal, false when the caller gets the notice.
	Worker bool `json:"worker,omitempty"`
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

// refusedStep marks a step of a refused hand-back. Tests stop the daemon there.
func (d *Daemon) refusedStep(step string) {
	if f := d.cfg.testHandbackRefusedStep; f != nil {
		f(step)
	}
}

// saveRefusal saves a refused hand-back before anyone is told. workerCanFix
// is false for Codex, whose hand-back the daemon builds itself. A refusal that
// finds an earlier one still saved goes to the caller: the worker had its
// message, or the caller's notice is still owed.
func (d *Daemon) saveRefusal(w *worker, req protocol.WorkerHandbackRequest, err error, turn string, workerCanFix bool) refusedHandback {
	_, earlier := d.loadRefusedHandback(w.id)
	var fixable handbackFixable
	r := refusedHandback{Req: req, Reason: cleanFailure(err.Error()), Turn: turn, NoticeKey: protocol.NewID("k"),
		Worker: workerCanFix && !earlier && errors.As(err, &fixable)}
	if err := d.saveRefusedHandback(w.id, r); err != nil {
		d.Logf("worker %s: save the refused hand-back: %v", w.id, err)
	}
	d.Logf("worker.handback_refused worker=%s tell_worker=%t reason=%q", w.id, r.Worker, r.Reason)
	return r
}

// tellWorkerRefused sends the worker its one message about a refusal it can
// correct, through the same path as an automatic continue.
func (d *Daemon) tellWorkerRefused(ctx context.Context, w *worker, reason string) error {
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	m := protocol.Message{ID: protocol.NewID("m"), From: "agent-net", FromName: "agent-net", FromHarness: "agent-net",
		To: sid, Type: protocol.TypeNotification, Body: handbackRefusedText(reason)}
	return d.deliverAutoContinue(ctx, w, m)
}

func handbackRefusedText(reason string) string {
	return "agent-net could not take your hand-back: " + reason +
		"\nYour report is kept. Fix the cause and call agent_handback again. If you cannot fix it, hand back with status blocked and say why." +
		"\nThis is the only message about it: if your next hand-back is refused too, or you end your turn without one, your caller gets your report and the reason."
}

// tellCallerRefused sends the caller the worker's report and the reason, with
// the saved key. The saved report is removed once the notice is sent.
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
	if _, err := d.sendNoticeToKey(ctx, w, n, r.Req.Report, "", "", r.NoticeKey, false); err != nil {
		d.Logf("worker %s: refused hand-back notice: %v", w.id, err)
		return false
	}
	d.refusedStep("told")
	d.dropRefusedHandback(w.id)
	return true
}

// codexRefused handles a refused Codex hand-back. Each step is on disk before
// the next one starts:
//
//   - refused: nothing is saved. Adoption hands the turn back again.
//   - saved: the record is saved. Adoption skips the turn and sends the notice.
//   - idle: the idle period is saved as told, so no idle notice follows and
//     adoption still skips the turn once the record is gone.
//   - told: the notice is sent. Adoption sends it again with the same key,
//     and the gateway keeps the first.
//
// Then the record is removed, and the idle period alone marks the turn.
func (d *Daemon) codexRefused(w *worker, req protocol.WorkerHandbackRequest, final string, err error) {
	d.refusedStep("refused")
	r := d.saveRefusal(w, req, err, "", false)
	d.refusedStep("saved")
	d.noteCodexIdle(w, final, true)
	d.refusedStep("idle")
	d.tellCallerRefused(context.Background(), w, r)
}

// removeLostWorkerFolder removes the folder of a worker whose process ended
// while the daemon was down. A refused hand-back stays: the lost notice at
// the next connect carries it, and the state sweep removes the folder later.
func (d *Daemon) removeLostWorkerFolder(workerID string) {
	dir := filepath.Join(d.cfg.StateDir, "workers", workerID)
	if _, ok := d.loadRefusedHandback(workerID); !ok {
		os.RemoveAll(dir)
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != filepath.Base(d.refusedHandbackPath(workerID)) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
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
