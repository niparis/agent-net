package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The caller may withdraw the ask of a held start at any time. A confirm and a
// withdraw can race, so the withdraw must stop a start that was confirmed but
// has not begun, and must say so when the worker already runs (decision 0151).

// withdrawSettleWait is how long a withdraw waits for a start that is in the
// judge or at the gateway to settle before it answers.
var withdrawSettleWait = 15 * time.Second

// withdrawableKey marks the context of a confirmed start, which a withdraw may
// cancel until the gateway has the start.
type withdrawableKey struct{}

// errStartWithdrawn ends a confirmed start that its caller withdrew first.
var errStartWithdrawn = errors.New("the caller withdrew the start before it ran")

// noteConfirmedStart records a start that a confirm released from the hold and
// returns the context it runs in.
func (d *Daemon) noteConfirmedStart(id string) context.Context {
	ctx, cancel := context.WithCancel(context.WithValue(d.ctoCtx, withdrawableKey{}, true))
	d.acceptedMu.Lock()
	defer d.acceptedMu.Unlock()
	if d.accepted == nil {
		d.accepted = map[string]*acceptedStart{}
	}
	d.accepted[id] = &acceptedStart{cancel: cancel, done: make(chan struct{})}
	return ctx
}

// settleConfirmedStart records how a confirmed start ended. It tells whether
// the caller withdrew it.
func (d *Daemon) settleConfirmedStart(id string, res *protocol.WorkerStartResult, err error) bool {
	d.acceptedMu.Lock()
	if e := d.accepted[id]; e != nil {
		e.cancelled = errors.Is(err, errStartWithdrawn)
		if e.cancel != nil {
			e.cancel()
		}
	}
	d.acceptedMu.Unlock()
	d.settleAccepted(id, res, err)
	d.acceptedMu.Lock()
	defer d.acceptedMu.Unlock()
	e := d.accepted[id]
	return e != nil && e.withdrawn
}

// withdrawnStartNote is what the caller hears when it withdraws the ask of a
// held start. It stops the start when the worker has not begun.
func (d *Daemon) withdrawnStartNote(ctx context.Context, caller, id string) string {
	const cancelled = "withdrawn; the start was cancelled before it ran"
	d.heldMu.Lock()
	_, held := d.heldStarts[id]
	delete(d.heldStarts, id)
	if held {
		// The answer may already be on its way; onStartDecision drops it.
		if d.withdrawnStarts == nil {
			d.withdrawnStarts = map[string]time.Time{}
		}
		now := time.Now()
		for old, at := range d.withdrawnStarts {
			if now.Sub(at) > time.Hour {
				delete(d.withdrawnStarts, old)
			}
		}
		d.withdrawnStarts[id] = now
	}
	d.heldMu.Unlock()
	if held {
		d.Logf("worker.withdrawn start=%s: held, dropped", id)
		return cancelled
	}
	d.acceptedMu.Lock()
	e := d.accepted[id]
	if e == nil {
		d.acceptedMu.Unlock()
		return "withdrawn; this machine does not track the start (it restarted, or the start was never held here), so nothing was cancelled. Check agent_workers_find for a worker of this start"
	}
	e.withdrawn = true
	settled, done, cancel := e.Settled, e.done, e.cancel
	d.acceptedMu.Unlock()
	if !settled && done != nil {
		if cancel != nil {
			cancel()
		}
		select {
		case <-done:
		case <-time.After(withdrawSettleWait):
			return "withdrawn, but the start is still being launched and could not be cancelled yet. Look for its worker with agent_workers_find and stop it with agent_worker_stop"
		case <-ctx.Done():
		}
	}
	d.acceptedMu.Lock()
	st := *e
	d.acceptedMu.Unlock()
	switch {
	case st.Session != "":
		d.Logf("worker.withdrawn start=%s: already started as %s", id, st.Session)
		return "already started as " + st.Session + "; stop it with agent_worker_stop"
	case st.Queue != "":
		cctx := withCallWait(ctx, 30*time.Second)
		if _, err := d.call(cctx, "worker.stop", protocol.WorkerStopFrame{Session: st.Queue, Requester: caller}); err != nil {
			// The queued start may have begun while the withdraw ran.
			d.acceptedMu.Lock()
			session := e.Session
			d.acceptedMu.Unlock()
			if session != "" {
				return "already started as " + session + "; stop it with agent_worker_stop"
			}
			return fmt.Sprintf("withdrawn, but the queued start %s could not be cancelled (%v). Stop it with agent_worker_stop", st.Queue, tooOldGateway(err))
		}
		d.acceptedMu.Lock()
		e.Queue = ""
		d.acceptedMu.Unlock()
		d.Logf("worker.withdrawn start=%s: queued start %s cancelled", id, st.Queue)
		return cancelled
	case st.cancelled:
		d.Logf("worker.withdrawn start=%s: cancelled before it ran", id)
		return cancelled
	}
	return "withdrawn; the start had already failed, so nothing was running"
}
