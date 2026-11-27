package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// movedToNextModelNote ends the notice of a worker whose model this machine
// could not run, when its start goes on with another model.
const movedToNextModelNote = "This machine cannot run that model, so the start moves to the next model the gate picks. A new notice follows"

// firstUntouchedRunLocked tells whether w is an automatic start that has not
// done anything yet: no continue, fix or hand-back. Only such a start can move
// to another model without losing work. The caller holds Daemon.wmu.
func firstUntouchedRunLocked(w *worker) bool {
	return w.launch != nil && w.launch.Automatic && w.launch.TaskID == "" &&
		w.continueCount == 0 && w.fixCount == 0 && w.autoContinueCount == 0 &&
		w.doneStatus == "" && !w.stopping && !w.movesToNextModel
}

// moveToNextModel ends a worker whose run failed because this machine cannot
// run its model, and puts its task back in the queue under a new start. The
// gateway marks the model unavailable here and tracks the new start; the
// queue then asks the gate for a model, which leaves this one out (decision
// 0162). When the gateway does not take the report (an older gateway, or no
// link), the worker ends with the failure as before.
func (d *Daemon) moveToNextModel(ctx context.Context, w *worker, reason, failure string) {
	d.wmu.Lock()
	l := *w.launch
	d.wmu.Unlock()
	cctx, cancel := context.WithTimeout(withCallWait(ctx, 15*time.Second), 15*time.Second)
	raw, err := d.call(cctx, "worker.model_unavailable", protocol.WorkerModelUnavailable{Ref: l.StartRef, Owner: l.Owner,
		Project: l.Project, Harness: l.Harness, Model: l.Model, Failure: failure})
	cancel()
	var answer protocol.WorkerModelUnavailableAnswer
	if err == nil {
		err = json.Unmarshal(raw, &answer)
	}
	if err == nil && answer.Ref == "" {
		err = errors.New("the gateway named no new start")
	}
	d.wmu.Lock()
	w.movesToNextModel = err == nil
	d.wmu.Unlock()
	d.beginWorkerEnd(w, reason, nil)
	if err != nil {
		d.Logf("worker.model_unavailable start=%s harness=%s model=%s: %v (the start ends here)", l.StartRef, l.Harness, l.Model, err)
		return
	}
	e := queuedFromLaunch(l, answer.Ref)
	// A stop that came while the gateway answered is final: the task does
	// not go on with the next model.
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	if why := d.st.noRestart(sid); why == noRestartStopped {
		d.notStarted(ctx, e, noRestartReason(sid, why))
		return
	}
	if err := d.st.addQueued(e); err != nil {
		d.notStarted(ctx, e, "the machine could not run "+l.Model+", and the start could not be queued for the next model: "+err.Error())
		return
	}
	d.Logf("worker.model_unavailable start=%s harness=%s model=%s next_start=%s", l.StartRef, l.Harness, l.Model, answer.Ref)
	d.processQueue(ctx)
}
